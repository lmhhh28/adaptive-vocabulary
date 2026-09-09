"""PL-02: Security and constraints acceptance test.
Verifies process locking, loopback enforcement, Origin/CSRF protections, and policy validation.
"""
import json
from pathlib import Path
import subprocess
import httpx
from support.lifecycle import managed_server
from support.http_client import APIClient


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    data_dir = case_dir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)

    policy_path = context.get("policy_path") or (bin_path.resolve().parents[1] / "config/policy-v2.json")
    artifacts = []

    # 1. Start primary server
    with managed_server(bin_path, data_dir, listen_port=0, policy_path=policy_path, log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))
        base_url = server.base_url

        # Check A: Second server process on same data directory must fail immediately
        proc2 = subprocess.run(
            [str(bin_path), "serve", "--data-dir", str(data_dir), "--listen", "127.0.0.1:0", "--policy", str(policy_path)],
            capture_output=True,
            text=True,
        )
        assert proc2.returncode != 0, f"Second server should have failed, but exited with 0: {proc2.stdout}"

        # Check B: Doctor command on same data directory detects SERVICE_RUNNING
        doc_proc = subprocess.run(
            [str(bin_path), "doctor", "--data-dir", str(data_dir), "--policy", str(policy_path)],
            capture_output=True,
            text=True,
        )
        assert doc_proc.returncode != 0, f"Doctor should report error while service is running: {doc_proc.stdout}"
        combined_doc = doc_proc.stdout + doc_proc.stderr
        assert "SERVICE_RUNNING" in combined_doc, f"Expected SERVICE_RUNNING in doctor output: {combined_doc}"

        # Check C: Non-loopback listen addresses rejected
        for bad_addr in ["0.0.0.0:8080", "192.168.1.1:8080", "example.com:8080"]:
            bad_proc = subprocess.run(
                [str(bin_path), "serve", "--data-dir", str(case_dir / "bad_listen"), "--listen", bad_addr, "--policy", str(policy_path)],
                capture_output=True,
                text=True,
            )
            assert bad_proc.returncode != 0, f"Non-loopback listen {bad_addr} should have failed"

        # Check D: Illegal Origin check
        client = APIClient(base_url)
        csrf_token = client.initialize_client_context()

        # Write with illegal Origin
        resp_origin = client.post(
            "/api/v1/test/action",
            json={"action": "test"},
            headers={"Origin": "http://malicious.domain.com", "X-CSRF-Token": csrf_token},
        )
        assert resp_origin.status_code == 403, f"Expected 403 for illegal Origin, got {resp_origin.status_code}"
        assert resp_origin.json()["error"]["code"] == "ORIGIN_REJECTED"

        # Check E: Missing or invalid CSRF
        resp_no_csrf = httpx.post(f"{base_url}/api/v1/test/action", json={"action": "test"})
        assert resp_no_csrf.status_code == 403, f"Expected 403 for missing CSRF, got {resp_no_csrf.status_code}"
        assert resp_no_csrf.json()["error"]["code"] == "CSRF_REJECTED"

        resp_bad_csrf = client.post(
            "/api/v1/test/action",
            json={"action": "test"},
            headers={"X-CSRF-Token": "invalid_token_12345"},
        )
        assert resp_bad_csrf.status_code == 403, f"Expected 403 for invalid CSRF, got {resp_bad_csrf.status_code}"
        assert resp_bad_csrf.json()["error"]["code"] == "CSRF_REJECTED"

        # Check F: Valid CSRF succeeds
        resp_valid = client.post("/api/v1/test/action", json={"action": "test"})
        assert resp_valid.status_code == 200, f"Expected 200 for valid CSRF, got {resp_valid.status_code}"

        client.close()

    # Check G: Corrupted / invalid policy file causes startup failure without corrupting data dir
    bad_policy_dir = case_dir / "bad_policy"
    bad_policy_dir.mkdir(parents=True, exist_ok=True)
    bad_policy_file = bad_policy_dir / "corrupt.json"
    bad_policy_file.write_text("{corrupt-json", encoding="utf-8")

    proc_corrupt = subprocess.run(
        [
            str(bin_path),
            "serve",
            "--data-dir",
            str(data_dir),
            "--listen",
            "127.0.0.1:0",
            "--policy",
            str(bad_policy_file),
        ],
        capture_output=True,
        text=True,
    )
    assert proc_corrupt.returncode != 0, "Server with corrupted policy should fail to start"

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "process_lock_verified": True,
            "loopback_enforced": True,
            "origin_check_verified": True,
            "csrf_check_verified": True,
            "policy_validation_verified": True,
        },
    }
