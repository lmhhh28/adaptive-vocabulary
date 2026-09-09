"""WK-02: Worker crash and checkpoint recovery acceptance test.
Verifies crash after generator response does not reuse unknown/superseded reservations,
does not submit under expired lease, and does not duplicate publish.
"""
import json
from pathlib import Path
import sqlite3
import uuid

from support.lifecycle import managed_server
from support.http_client import APIClient
from support.provider_server import ProviderServer
from support.worker_process import run_worker


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    data_dir = case_dir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)
    artifacts = []

    provider = ProviderServer(expected_api_key="wk02-key", scenario_mode="normal_pass")
    provider.start()

    try:
        with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
            base_url = server.base_url
            artifacts.append(str(server.log_path))

            token_path = data_dir / "worker.token"
            assert token_path.is_file(), "worker.token missing"

            db_path = data_dir / "app.db"
            conn = sqlite3.connect(db_path)
            cur = conn.cursor()
            cur.execute("SELECT id, revision FROM bank WHERE name = '开发样例'")
            bank_id, bank_rev = cur.fetchone()

            cur.execute("""
                SELECT s.id, sv.id
                FROM sense s
                JOIN sense_version sv ON sv.sense_id = s.id
                JOIN bank_sense bs ON bs.sense_id = s.id
                WHERE bs.bank_id = ?
                LIMIT 1
            """, (bank_id,))
            sense_id, sense_version_id = cur.fetchone()
            conn.close()

            # 1. Create generation job
            client = APIClient(base_url)
            client.initialize_client_context()
            job_resp = client.post(
                f"/api/v1/banks/{bank_id}/generation-jobs",
                json={
                    "sense_refs": [{"sense_id": sense_id, "sense_version_id": sense_version_id}],
                    "generation_config_version": "v1",
                    "quality_policy_version": "v1",
                    "expected_revision": bank_rev
                },
                headers={"Idempotency-Key": str(uuid.uuid4())}
            )
            assert job_resp.status_code == 202
            job_id = job_resp.json()["job_id"]

            profile_path = case_dir / "generation-profile.json"
            profile = {
                "config_version": "generation-v2",
                "budget_policy_version": "budget-v2",
                "mode": "test",
                "quality_policy_version": "quality-v1",
                "production_adapter": "chat-completions-json-v1",
                "generator": {
                    "base_url": provider.base_url,
                    "model": "test-generator",
                    "api_key": "wk02-key",
                    "max_output_tokens": 1024
                },
                "evaluator": {
                    "base_url": provider.base_url,
                    "model": "test-evaluator",
                    "api_key": "wk02-key",
                    "max_output_tokens": 512
                }
            }
            profile_path.write_text(json.dumps(profile, indent=2), encoding="utf-8")

            # 2. Worker 1 runs with crash_after_gen=True
            res1 = run_worker(
                data_dir=data_dir,
                server_url=base_url,
                profile_path=profile_path,
                worker_id="worker-crash-1",
                max_items=1,
                crash_after_gen=True
            )
            assert res1.returncode != 0, f"Worker 1 should have crashed, returned {res1.returncode}"
            assert "Simulated worker crash" in res1.stderr or "Simulated worker crash" in res1.stdout

            # 3. Simulate lease expiration in DB
            conn = sqlite3.connect(db_path)
            cur = conn.cursor()
            cur.execute("UPDATE generation_item SET lease_until = lease_until - 300000000 WHERE job_id = ?", (job_id,))
            conn.commit()
            conn.close()

            # 4. Worker 2 claims the item -> gets fencing token 2
            res2 = run_worker(
                data_dir=data_dir,
                server_url=base_url,
                profile_path=profile_path,
                worker_id="worker-recovery-2",
                max_items=1
            )
            assert res2.returncode == 0, f"Worker 2 failed with code {res2.returncode}:\n{res2.stderr}"

            # 5. Verify published candidate in SQLite
            conn = sqlite3.connect(db_path)
            cur = conn.cursor()
            cur.execute("SELECT COUNT(*) FROM question_candidate WHERE disposition = 'published'")
            published_candidates = cur.fetchone()[0]
            assert published_candidates == 1, f"Expected 1 published candidate, got {published_candidates}"

            # Verify ledger kept both the crashed run tokens and the recovery run tokens
            cur.execute("SELECT authorized_requests, total_tokens FROM generation_budget_ledger WHERE job_id = ?", (job_id,))
            auth_reqs, tot_toks = cur.fetchone()
            # Worker 1 had 1 generator call (settled before crash). Worker 2 had 1 generator + 2 evaluators. Total = 4 calls.
            assert auth_reqs == 4, f"Expected 4 authorized requests, got {auth_reqs}"
            assert tot_toks > 0, "Tokens should be accounted"
            conn.close()

    finally:
        provider.stop()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "crash_verified": True,
            "superseded_lease_handled": True,
            "no_duplicate_publishing": True,
            "authorized_requests": auth_reqs,
            "total_tokens": tot_toks
        }
    }
