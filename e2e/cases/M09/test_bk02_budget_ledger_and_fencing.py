"""BK-02: Budget ledger retention, fence invalidation and unknown call state acceptance test.
Verifies:
1. Restoring without checkpoint preserves budget ledger occupied allocations (authorized_requests, total_tokens).
2. Unfinished calls become unknown and are not reused.
3. Old worker with old fencing token cannot publish.
4. Item reclaimed with incremented fencing token and new attempt number.
5. Reaching max_requests triggers BUDGET_EXHAUSTED.
"""
import json
from pathlib import Path
import sqlite3
import subprocess
import uuid

import httpx
from support.lifecycle import managed_server
from support.http_client import APIClient


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    src_data_dir = case_dir / "src_data"
    src_data_dir.mkdir(parents=True, exist_ok=True)
    backup_dir = case_dir / "backup"
    restored_data_dir = case_dir / "restored_data"
    artifacts = []

    bank_id = "00000000-0000-4000-8000-000000000801"
    job_id = None
    item_id = None
    res_1 = None

    # 1. Start source server
    with managed_server(bin_path, src_data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))
        client = APIClient(server.base_url)
        client.initialize_client_context()

        # Find sense
        conn = sqlite3.connect(str(src_data_dir / "app.db"))
        cur = conn.cursor()
        cur.execute("""
            SELECT s.id, sv.id
            FROM sense s
            JOIN sense_version sv ON sv.sense_id = s.id
            JOIN bank_sense bs ON bs.sense_id = s.id
            WHERE bs.bank_id = ? LIMIT 1
        """, (bank_id,))
        sense_id, sense_version_id = cur.fetchone()
        conn.close()

        # Create job
        job_resp = client.post(
            f"/api/v1/banks/{bank_id}/generation-jobs",
            json={
                "sense_refs": [{"sense_id": sense_id, "sense_version_id": sense_version_id}],
                "generation_config_version": "v1",
                "quality_policy_version": "v1",
                "expected_revision": 0
            },
            headers={"Idempotency-Key": str(uuid.uuid4())}
        )
        assert job_resp.status_code == 202
        job_id = job_resp.json()["job_id"]

        # Limit budget to 2 requests max directly in SQLite
        conn = sqlite3.connect(str(src_data_dir / "app.db"))
        cur = conn.cursor()
        cur.execute("UPDATE generation_budget_ledger SET max_requests = 2, max_tokens = 2000 WHERE job_id = ?", (job_id,))
        conn.commit()
        conn.close()

        # Worker token
        token = (src_data_dir / "worker.token").read_text().strip()
        auth_headers = {"Authorization": f"Bearer {token}"}

        # Worker A claims item
        claim_resp = httpx.post(
            f"{server.base_url}/internal/v1/generation/claim",
            json={"worker_id": "worker-A", "max_items": 1},
            headers=auth_headers
        )
        assert claim_resp.status_code == 200
        items = claim_resp.json()["items"]
        assert len(items) == 1
        item = items[0]
        item_id = item["item_id"]
        fence_A = item["fencing_token"]
        assert fence_A == 1
        input_digest = item["input_digest"]

        # Worker A reserves call 1
        net_attempt_1 = str(uuid.uuid4())
        reserve_resp1 = httpx.post(
            f"{server.base_url}/internal/v1/generation/calls/reserve",
            json={
                "worker_id": "worker-A",
                "item_id": item_id,
                "fencing_token": fence_A,
                "input_digest": input_digest,
                "logical_call_key": {
                    "item_id": item_id,
                    "attempt_number": 1,
                    "stage": "generator",
                    "candidate_index": 0,
                    "evaluation_index": 0
                },
                "network_attempt_id": net_attempt_1,
                "profile_version": "generation-v2",
                "input_sha256": "fake-input-sha",
                "input_token_estimate": 200,
                "max_output_tokens": 300
            },
            headers=auth_headers
        )
        assert reserve_resp1.status_code == 200, f"Reserve failed: {reserve_resp1.text}"
        res_1 = reserve_resp1.json()["reservation_id"]
        assert reserve_resp1.json()["authorized_requests"] == 1
        assert reserve_resp1.json()["budget_remaining"]["requests"] == 1

        # Worker A crashes (leaves call reserved, does not settle)

    # 2. Create backup WITHOUT checkpoint
    create_cmd = [
        str(bin_path), "backup", "create",
        "--data-dir", str(src_data_dir),
        "--output", str(backup_dir)
    ]
    res_create = subprocess.run(create_cmd, capture_output=True, text=True)
    assert res_create.returncode == 0, f"backup create failed: {res_create.stderr}"

    # 3. Verify backup
    verify_cmd = [str(bin_path), "backup", "verify", "--input", str(backup_dir)]
    res_verify = subprocess.run(verify_cmd, capture_output=True, text=True)
    assert res_verify.returncode == 0, f"backup verify failed: {res_verify.stderr}"

    # 4. Restore to new directory
    restore_cmd = [
        str(bin_path), "backup", "restore",
        "--input", str(backup_dir),
        "--target", str(restored_data_dir)
    ]
    res_restore = subprocess.run(restore_cmd, capture_output=True, text=True)
    assert res_restore.returncode == 0, f"backup restore failed: {res_restore.stderr}"

    # 5. Start server on restored directory
    with managed_server(bin_path, restored_data_dir, listen_port=0, log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))
        token = (restored_data_dir / "worker.token").read_text().strip()
        auth_headers = {"Authorization": f"Bearer {token}"}

        # Verify in SQLite: call status is unknown and budget is still occupied
        conn = sqlite3.connect(str(restored_data_dir / "app.db"))
        cur = conn.cursor()
        cur.execute("SELECT status FROM generation_call WHERE reservation_id = ?", (res_1,))
        c_status = cur.fetchone()[0]
        assert c_status == "unknown", f"Expected call status unknown, got {c_status}"

        cur.execute("SELECT authorized_requests, total_tokens FROM generation_budget_ledger WHERE job_id = ?", (job_id,))
        auth_reqs, tot_toks = cur.fetchone()
        assert auth_reqs == 1, f"Expected authorized_requests 1, got {auth_reqs}"
        assert tot_toks == 500, f"Expected total_tokens 500, got {tot_toks}"
        conn.close()

        # Old Worker A tries to publish candidate using old lease & old fence -> rejected
        pub_resp = httpx.post(
            f"{server.base_url}/internal/v1/generation/candidates",
            json={
                "worker_id": "worker-A",
                "item_id": item_id,
                "fencing_token": fence_A,
                "input_digest": input_digest,
                "candidate": {
                    "question_text": "Sample question?",
                    "options": [
                        {"option_id": "o1", "text": "A", "is_correct": True},
                        {"option_id": "o2", "text": "B", "is_correct": False}
                    ],
                    "metadata": {
                        "generator_reservation_id": res_1,
                        "evaluator_1_reservation_id": "fake-eval-1",
                        "evaluator_2_reservation_id": "fake-eval-2"
                    }
                }
            },
            headers=auth_headers
        )
        assert pub_resp.status_code in (400, 409), f"Expected lease error for old worker, got {pub_resp.status_code}: {pub_resp.text}"

        # Worker B claims the item -> receives fence 2, attempt 2
        claim_resp2 = httpx.post(
            f"{server.base_url}/internal/v1/generation/claim",
            json={"worker_id": "worker-B", "max_items": 1},
            headers=auth_headers
        )
        assert claim_resp2.status_code == 200
        items2 = claim_resp2.json()["items"]
        assert len(items2) == 1
        item2 = items2[0]
        assert item2["item_id"] == item_id
        fence_B = item2["fencing_token"]
        assert fence_B > fence_A, f"Expected fencing_token > {fence_A}, got {fence_B}"
        attempt_B = item2["attempt_number"]
        assert attempt_B >= 2, f"Expected attempt_number >= 2, got {attempt_B}"

        # Worker B reserves request 2 (which reaches max_requests = 2)
        net_attempt_2 = str(uuid.uuid4())
        reserve_resp2 = httpx.post(
            f"{server.base_url}/internal/v1/generation/calls/reserve",
            json={
                "worker_id": "worker-B",
                "item_id": item_id,
                "fencing_token": fence_B,
                "input_digest": item2["input_digest"],
                "logical_call_key": {
                    "item_id": item_id,
                    "attempt_number": attempt_B,
                    "stage": "generator",
                    "candidate_index": 0,
                    "evaluation_index": 0
                },
                "network_attempt_id": net_attempt_2,
                "profile_version": "generation-v2",
                "input_sha256": "fake-input-sha",
                "input_token_estimate": 200,
                "max_output_tokens": 300
            },
            headers=auth_headers
        )
        assert reserve_resp2.status_code == 200, f"Worker B reserve failed: {reserve_resp2.text}"
        assert reserve_resp2.json()["authorized_requests"] == 2
        assert reserve_resp2.json()["budget_remaining"]["requests"] == 0

        # Worker B tries to reserve a 3rd request -> rejected with BUDGET_EXHAUSTED
        net_attempt_3 = str(uuid.uuid4())
        reserve_resp3 = httpx.post(
            f"{server.base_url}/internal/v1/generation/calls/reserve",
            json={
                "worker_id": "worker-B",
                "item_id": item_id,
                "fencing_token": fence_B,
                "input_digest": item2["input_digest"],
                "logical_call_key": {
                    "item_id": item_id,
                    "attempt_number": attempt_B,
                    "stage": "evaluator",
                    "candidate_index": 0,
                    "evaluation_index": 0
                },
                "network_attempt_id": net_attempt_3,
                "profile_version": "generation-v2",
                "input_sha256": "fake-input-sha",
                "input_token_estimate": 100,
                "max_output_tokens": 100
            },
            headers=auth_headers
        )
        assert reserve_resp3.status_code == 409, f"Expected 409 BUDGET_EXHAUSTED, got {reserve_resp3.status_code}: {reserve_resp3.text}"
        assert "BUDGET_EXHAUSTED" in reserve_resp3.text

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "job_id": job_id,
            "item_id": item_id,
            "unknown_reservation_id": res_1
        }
    }
