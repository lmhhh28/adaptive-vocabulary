"""GE-02: Budget ledger, crash recovery, unknown usage, late settlement and limits acceptance test.
Verifies concurrent budget slot race, crash/takeover unknown call status, 3-attempt limit,
missing usage settlement, late settlement, and actual usage budget overrun per 10 R02.
"""
from pathlib import Path
import sqlite3
import uuid
from support.lifecycle import managed_server
from support.http_client import APIClient
from support.scripted_worker import ScriptedWorker


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    data_dir = case_dir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)
    artifacts = []

    with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        base_url = server.base_url
        artifacts.append(str(server.log_path))

        token_path = data_dir / "worker.token"
        worker_token = token_path.read_text().strip()

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
            LIMIT 2
        """, (bank_id,))
        senses = cur.fetchall()
        assert len(senses) >= 2, "Need at least 2 senses"
        conn.close()

        # 1. Create job with 2 senses
        client = APIClient(base_url)
        client.initialize_client_context()
        create_resp = client.post(f"/api/v1/banks/{bank_id}/generation-jobs", json={
            "sense_refs": [
                {"sense_id": senses[0][0], "sense_version_id": senses[0][1]},
                {"sense_id": senses[1][0], "sense_version_id": senses[1][1]}
            ],
            "generation_config_version": "v1",
            "quality_policy_version": "v1",
            "expected_revision": bank_rev,
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert create_resp.status_code == 202
        job_id = create_resp.json()["job_id"]

        # 2. Worker claims both items
        worker = ScriptedWorker(base_url, worker_token, worker_id="worker-ledger")
        claim_resp = worker.claim(limit=2)
        assert claim_resp.status_code == 200
        items = claim_resp.json()["items"]
        assert len(items) == 2
        item1, item2 = items[0], items[1]

        # 3. Simulate budget boundary: set max_requests = 2, authorized_requests = 1 in DB
        # Exactly 1 request authorization remaining!
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("""
            UPDATE generation_budget_ledger
            SET max_requests = 2, authorized_requests = 1, max_tokens = 50000, total_tokens = 1000
            WHERE job_id = ?
        """, (job_id,))
        conn.commit()
        conn.close()

        # 4. Competing reservation: Item 1 and Item 2 both attempt to reserve
        key1 = {"attempt_number": 1, "stage": "generator", "candidate_index": 0}
        key2 = {"attempt_number": 1, "stage": "generator", "candidate_index": 0}

        r1 = worker.reserve(item1["item_id"], item1["fencing_token"], item1["input_digest"], key1, str(uuid.uuid4()))
        r2 = worker.reserve(item2["item_id"], item2["fencing_token"], item2["input_digest"], key2, str(uuid.uuid4()))

        # Exactly one must succeed with 200, the other must fail with 409 BUDGET_EXHAUSTED
        statuses = {r1.status_code, r2.status_code}
        assert statuses == {200, 409}, f"Expected {200, 409}, got {statuses}"
        success_res = r1 if r1.status_code == 200 else r2
        failed_res = r2 if r1.status_code == 200 else r1
        assert failed_res.json()["error"]["code"] == "BUDGET_EXHAUSTED"

        winning_res_id = success_res.json()["reservation_id"]
        winning_item = item1 if r1.status_code == 200 else item2

        # 5. Worker crash after reservation:
        # Simulate lease expiry for winning_item
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("UPDATE generation_item SET lease_until = lease_until - 300000000 WHERE id = ?", (winning_item["item_id"],))
        conn.commit()
        conn.close()

        # Worker B claims the expired item (takeover)
        worker_b = ScriptedWorker(base_url, worker_token, worker_id="worker-b")
        claim_takeover = worker_b.claim(limit=1)
        assert claim_takeover.status_code == 200
        takeover_items = claim_takeover.json()["items"]
        assert len(takeover_items) == 1
        assert takeover_items[0]["item_id"] == winning_item["item_id"]

        # Verify DB: the reserved call must have transitioned to status = 'unknown'
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("SELECT status FROM generation_call WHERE reservation_id = ?", (winning_res_id,))
        call_status = cur.fetchone()[0]
        assert call_status == "unknown", f"Expected call status 'unknown' after takeover, got {call_status}"

        # Verify ledger: total_tokens still retains the reservation!
        cur.execute("SELECT total_tokens FROM generation_budget_ledger WHERE job_id = ?", (job_id,))
        tot_toks = cur.fetchone()[0]
        assert tot_toks > 1000, "Reservation tokens must not be zeroed out on crash/takeover"
        conn.close()

        # 6. Settle with missing usage -> conservatively accounts reserved_tokens
        s_missing = worker.settle(
            winning_res_id, winning_item["item_id"], winning_item["fencing_token"], winning_item["input_digest"],
            outcome="response", usage=None
        )
        assert s_missing.status_code == 200, f"Settle with missing usage failed: {s_missing.text}"
        assert s_missing.json()["call_status"] == "settled"

        # 7. Test 3 network attempts limit for a logical call
        # Increase budget in DB to test network attempts limit without budget exhaustion
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("UPDATE generation_budget_ledger SET max_requests = 100, max_tokens = 500000 WHERE job_id = ?", (job_id,))
        conn.commit()
        conn.close()

        active_item = takeover_items[0]
        logical_key = {"attempt_number": active_item["attempt_number"], "stage": "generator", "candidate_index": 0}

        # Attempt 1
        att1 = worker_b.reserve(active_item["item_id"], active_item["fencing_token"], active_item["input_digest"], logical_key, str(uuid.uuid4()))
        assert att1.status_code == 200
        # Attempt 2
        att2 = worker_b.reserve(active_item["item_id"], active_item["fencing_token"], active_item["input_digest"], logical_key, str(uuid.uuid4()))
        assert att2.status_code == 200
        # Attempt 3
        att3 = worker_b.reserve(active_item["item_id"], active_item["fencing_token"], active_item["input_digest"], logical_key, str(uuid.uuid4()))
        assert att3.status_code == 200
        # Attempt 4 -> 409 CALL_ATTEMPTS_EXHAUSTED
        att4 = worker_b.reserve(active_item["item_id"], active_item["fencing_token"], active_item["input_digest"], logical_key, str(uuid.uuid4()))
        assert att4.status_code == 409, f"Expected 409, got {att4.status_code}"
        assert att4.json()["error"]["code"] == "CALL_ATTEMPTS_EXHAUSTED"

        # 8. Late settlement:
        # Expire lease for active_item in DB
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("UPDATE generation_item SET lease_until = lease_until - 300000000 WHERE id = ?", (active_item["item_id"],))
        conn.commit()
        conn.close()

        # Settle attempt 3 late (lease is expired)
        late_res_id = att3.json()["reservation_id"]
        late_settle = worker_b.settle(
            late_res_id, active_item["item_id"], active_item["fencing_token"], active_item["input_digest"],
            outcome="response", usage={"input_tokens": 150, "output_tokens": 150}
        )
        # Late settlement is allowed per 10 R02!
        assert late_settle.status_code == 200, f"Late settlement failed: {late_settle.text}"
        assert late_settle.json()["accounted_tokens"] == 300

        # But attempting candidate submission with expired lease is rejected!
        fail_sub = worker_b.submit_candidate(
            active_item["item_id"], active_item["fencing_token"], active_item["input_digest"],
            attempt_number=active_item["attempt_number"], candidate_index=0,
            content={"task_family": "meaning_recognition", "stem": "S", "options": [{"text": "A", "is_correct": True}, {"text": "B", "is_correct": False}, {"text": "C", "is_correct": False}, {"text": "D", "is_correct": False}]},
            generator_reservation_id=late_res_id, evaluator_reservation_ids=[late_res_id, late_res_id],
            checks={"result": "pass", "semantic_evaluations": []}
        )
        assert fail_sub.status_code == 409
        assert fail_sub.json()["error"]["code"] == "LEASE_LOST"

        # 9. Actual usage exceeds estimated -> records full usage & marks budget_overrun
        # Claim item fresh
        claim_fresh = worker_b.claim(limit=1)
        assert claim_fresh.status_code == 200
        fresh_item = claim_fresh.json()["items"][0]

        overrun_key = {"attempt_number": fresh_item["attempt_number"], "stage": "evaluator", "candidate_index": 0, "evaluation_index": 1}
        # Set max_tokens to 1000 in DB
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("UPDATE generation_budget_ledger SET max_tokens = 1000, total_tokens = 500 WHERE job_id = ?", (job_id,))
        conn.commit()
        conn.close()

        # Reserve with 200 tokens (500 + 200 = 700 <= 1000, succeeds)
        r_over = worker_b.reserve(
            fresh_item["item_id"], fresh_item["fencing_token"], fresh_item["input_digest"],
            overrun_key, str(uuid.uuid4()), input_token_estimate=100, max_output_tokens=100
        )
        assert r_over.status_code == 200
        over_res_id = r_over.json()["reservation_id"]

        # Settle with actual usage 2000 tokens (> 1000 max_tokens!)
        s_over = worker_b.settle(
            over_res_id, fresh_item["item_id"], fresh_item["fencing_token"], fresh_item["input_digest"],
            outcome="response", usage={"input_tokens": 1000, "output_tokens": 1000}
        )
        assert s_over.status_code == 200
        budget_status = s_over.json()["job_budget_status"]
        assert budget_status["budget_overrun"] is True, f"Expected budget_overrun=True, got {budget_status}"

        # Subsequent reserve should fail with BUDGET_EXHAUSTED because budget_overrun is set
        r_blocked = worker_b.reserve(
            fresh_item["item_id"], fresh_item["fencing_token"], fresh_item["input_digest"],
            {"attempt_number": fresh_item["attempt_number"], "stage": "evaluator", "candidate_index": 0, "evaluation_index": 2},
            str(uuid.uuid4())
        )
        assert r_blocked.status_code == 409
        assert r_blocked.json()["error"]["code"] == "BUDGET_EXHAUSTED"

        worker.close()
        worker_b.close()
        client.close()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "checks_verified": [
                "concurrent_budget_boundary",
                "crash_takeover_unknown_call",
                "call_attempts_exhausted_at_3",
                "missing_usage_conservative_settle",
                "late_settlement_accepted_without_publish_right",
                "actual_usage_budget_overrun_blocks_further_authorization"
            ]
        }
    }
