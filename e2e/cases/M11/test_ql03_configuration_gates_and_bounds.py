"""QL-03: Configuration Gates and Bounds Acceptance Test.

Verifies that:
1. Missing profile, missing API key, or disabled profile returns blocked_configuration
   with a non-zero exit code (code 2), rather than silently skipping or falsely passing.
2. The core fixture application continues to start, serve health, and handle practice sessions
   cleanly without any production profile or LLM keys configured.
"""
import json
import os
from pathlib import Path
import subprocess
import sys
import uuid

from support.lifecycle import managed_server
from support.http_client import APIClient

ROOT = Path(__file__).resolve().parents[3]


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    data_dir = case_dir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)
    artifacts = []

    uv_path = ROOT / ".tools/uv/bin/uv"
    python_cmd = [str(uv_path), "run", "--directory", "worker", "--locked", "python", "-m", "avocab_worker.quality_benchmark"]

    # 1. Gate 1: Nonexistent profile file
    p1 = subprocess.run(
        python_cmd + ["--profile", "config/nonexistent-profile.json"],
        cwd=ROOT,
        capture_output=True,
        text=True
    )
    assert p1.returncode != 0, f"Expected non-zero exit for nonexistent profile, got {p1.returncode}"
    assert "blocked_configuration" in p1.stderr.lower() or "blocked_configuration" in p1.stdout.lower(), (
        f"Expected blocked_configuration in output, got: {p1.stderr}\n{p1.stdout}"
    )

    # 2. Gate 2: Disabled profile (enabled=false)
    disabled_profile_path = case_dir / "disabled-profile.json"
    disabled_profile_path.write_text(
        json.dumps({
            "config_version": "generation-v2",
            "enabled": False,
            "mode": "production"
        }),
        encoding="utf-8"
    )
    p2 = subprocess.run(
        python_cmd + ["--profile", str(disabled_profile_path)],
        cwd=ROOT,
        capture_output=True,
        text=True
    )
    assert p2.returncode != 0, f"Expected non-zero exit for disabled profile, got {p2.returncode}"
    assert "blocked_configuration" in p2.stderr.lower() or "blocked_configuration" in p2.stdout.lower(), (
        f"Expected blocked_configuration in output, got: {p2.stderr}\n{p2.stdout}"
    )

    # 3. Gate 3: Missing API key
    env_no_key = dict(os.environ)
    env_no_key["AVOCAB_EVALUATOR_API_KEY"] = ""
    p3 = subprocess.run(
        python_cmd + ["--profile", "config/production-profile.json"],
        cwd=ROOT,
        env=env_no_key,
        capture_output=True,
        text=True
    )
    assert p3.returncode != 0, f"Expected non-zero exit for empty API key, got {p3.returncode}"
    assert "blocked_configuration" in p3.stderr.lower() or "blocked_configuration" in p3.stdout.lower(), (
        f"Expected blocked_configuration in output, got: {p3.stderr}\n{p3.stdout}"
    )

    # 4. Gate 4: Fixture app boots and runs without any LLM configured
    with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))
        client = APIClient(server.base_url)
        client.initialize_client_context()

        # Check health
        health = client.get("/api/v1/health")
        assert health.status_code == 200, f"Health check failed: {health.text}"
        h_data = health.json()
        assert h_data["status"] == "ok"
        assert h_data["mode"] == "active"
        assert h_data["contract_version"] == "1.1.0"

        # Check bank list works
        banks_res = client.get("/api/v1/banks")
        assert banks_res.status_code == 200, f"Banks list failed: {banks_res.text}"
        banks = banks_res.json()["items"]
        assert len(banks) > 0, "Expected at least 1 seeded bank"
        bank_id = banks[0]["id"]

        # Start practice session with fixtures
        sess_res = client.post(
            "/api/v1/practice-sessions",
            json={"bank_id": bank_id},
            headers={"Idempotency-Key": str(uuid.uuid4())}
        )
        assert sess_res.status_code in (200, 201), f"Create session failed: {sess_res.text}"
        sess_id = sess_res.json()["session"]["id"]

        # Next question should be served from seed fixtures
        next_res = client.post(f"/api/v1/practice-sessions/{sess_id}/next", json={"expected_cursor": 0})
        assert next_res.status_code == 200, f"Next question failed: {next_res.text}"
        next_data = next_res.json()
        assert next_data["question"]["stem"], "Question stem should be present"

        # End practice session cleanly
        end_res = client.post(f"/api/v1/practice-sessions/{sess_id}/end", json={"expected_revision": 0, "reason": "ended"})
        assert end_res.status_code == 200, f"End session failed: {end_res.text}"

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "missing_profile_blocked": True,
            "disabled_profile_blocked": True,
            "missing_api_key_blocked": True,
            "fixture_app_unaffected": True
        }
    }
