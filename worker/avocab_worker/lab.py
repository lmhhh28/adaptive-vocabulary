"""Generation lab utility for local testing and manual module verification."""
import json
from pathlib import Path
import sqlite3
import subprocess
import sys
import time
import uuid

from .config import WorkerConfig
from .runner import WorkerRunner

ROOT = Path(__file__).resolve().parents[2]
SUPPORT_DIR = ROOT / "e2e" / "support"
if str(SUPPORT_DIR) not in sys.path:
    sys.path.insert(0, str(SUPPORT_DIR))

from provider_server import ProviderServer


def run_lab(data_dir_path: str | Path, server_port: int = 0) -> dict:
    data_dir = Path(data_dir_path).resolve()
    data_dir.mkdir(parents=True, exist_ok=True)

    print(f"=== Starting Generation Lab at {data_dir} ===")

    # 1. Start test provider server on loopback
    provider = ProviderServer(expected_api_key="test-key", scenario_mode="normal_pass")
    provider.start()
    print(f"-> Test Provider running on {provider.base_url}")

    # 2. Write test generation profile
    profile_path = data_dir / "test-generation-profile.json"
    profile_content = {
        "config_version": "generation-v2",
        "budget_policy_version": "budget-v2",
        "mode": "test",
        "quality_policy_version": "quality-v1",
        "production_adapter": "chat-completions-json-v1",
        "generator": {
            "base_url": provider.base_url,
            "model": "test-generator",
            "api_key": "test-key",
            "max_output_tokens": 1024
        },
        "evaluator": {
            "base_url": provider.base_url,
            "model": "test-evaluator",
            "api_key": "test-key",
            "max_output_tokens": 512
        }
    }
    profile_path.write_text(json.dumps(profile_content, indent=2), encoding="utf-8")

    # 3. Start Go server if not already running
    bin_path = ROOT / "bin" / "avocab-e2e"
    if not bin_path.is_file():
        bin_path = ROOT / "bin" / "avocab"
    if not bin_path.is_file():
        raise FileNotFoundError(f"Binary not found at {bin_path}. Run 'make build' first.")

    cmd = [
        str(bin_path), "serve",
        "--data-dir", str(data_dir),
        "--listen", f"127.0.0.1:{server_port}",
        "--policy", str(ROOT / "config/policy-v2.json"),
        "--seed", "core"
    ]
    proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)

    ready_line = proc.stdout.readline()
    try:
        ready_data = json.loads(ready_line)
        base_url = ready_data["base_url"]
        print(f"-> Go Server running on {base_url} (pid {ready_data['pid']})")
    except Exception as exc:
        proc.kill()
        provider.stop()
        raise RuntimeError(f"Go server failed to start: {ready_line}, error: {exc}")

    try:
        token_path = data_dir / "worker.token"
        assert token_path.is_file(), "worker.token missing"
        worker_token = token_path.read_text().strip()

        # 4. Get a sense from DB to create a test job
        db_path = data_dir / "app.db"
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("SELECT id, revision FROM bank WHERE name = '开发样例'")
        bank_row = cur.fetchone()
        bank_id, bank_rev = bank_row[0], bank_row[1]

        cur.execute("""
            SELECT s.id, sv.id
            FROM sense s
            JOIN sense_version sv ON sv.sense_id = s.id
            JOIN bank_sense bs ON bs.sense_id = s.id
            WHERE bs.bank_id = ?
            LIMIT 1
        """, (bank_id,))
        sense_row = cur.fetchone()
        sense_id, sense_version_id = sense_row[0], sense_row[1]
        conn.close()

        # 5. Create generation job via HTTP
        import httpx
        client = httpx.Client(base_url=base_url, timeout=10.0)
        # Get CSRF and cookies
        ctx_resp = client.get("/client-context")
        csrf_token = ctx_resp.json().get("csrf_token", "")

        job_resp = client.post(
            f"/api/v1/banks/{bank_id}/generation-jobs",
            json={
                "sense_refs": [{"sense_id": sense_id, "sense_version_id": sense_version_id}],
                "generation_config_version": "v1",
                "quality_policy_version": "v1",
                "expected_revision": bank_rev
            },
            headers={
                "Idempotency-Key": str(uuid.uuid4()),
                "X-CSRF-Token": csrf_token,
                "Origin": base_url
            }
        )
        assert job_resp.status_code == 202, f"Failed to create job: {job_resp.text}"
        job_id = job_resp.json()["job_id"]
        print(f"-> Created generation job {job_id}")

        # 6. Run Worker
        cfg = WorkerConfig.load(
            data_dir=data_dir,
            server_url=base_url,
            profile_path=profile_path,
            worker_id="lab-worker-1"
        )
        runner = WorkerRunner(cfg)
        print("-> Running worker to process job...")
        results = runner.run_once(limit=1)
        runner.close()

        print(f"-> Item processed with status: {[r.get('status') for r in results]}")

        # 7. Check job status from Go
        job_status = client.get(f"/api/v1/generation-jobs/{job_id}").json()
        print(f"-> Job status: {job_status}")

        summary = {
            "status": "ok",
            "job_id": job_id,
            "job_status": job_status,
            "worker_results": results,
            "provider_requests": provider.request_count
        }
        return summary
    finally:
        client.close()
        proc.terminate()
        try:
            proc.wait(timeout=3.0)
        except Exception:
            proc.kill()
        provider.stop()
        print("=== Generation Lab Finished ===")
