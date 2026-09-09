"""PR-04: Fault injection rollback and concurrency conflict acceptance test.
Verifies:
1. Mid-transaction failure rolls back attempt, review, feedback exposure, correction item, and cursor;
2. After session ended, subsequent next / attempts are rejected with SESSION_CLOSED;
3. No illegal question instances retained after closure.
"""
from pathlib import Path
import sqlite3
import uuid
from support.lifecycle import managed_server
from support.http_client import APIClient


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    data_dir = case_dir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)
    artifacts = []

    bank_id = "00000000-0000-4000-8000-000000000801"

    with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        base_url = server.base_url
        artifacts.append(str(server.log_path))

        client = APIClient(base_url)
        client.initialize_client_context()

        # 1. Create practice session
        resp = client.post("/api/v1/practice-sessions", json={"bank_id": bank_id}, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp.status_code == 201
        session_id = resp.json()["session"]["id"]

        # 2. Get first question (cursor 0)
        resp_next = client.post(f"/api/v1/practice-sessions/{session_id}/next", json={"expected_cursor": 0})
        assert resp_next.status_code == 200
        q0 = resp_next.json()["question"]
        inst_0_id = q0["instance_id"]
        opt_0_id = q0["options"][0]["id"]

        # 3. Submit attempt with X-Test-Fault: fail_after_attempt
        resp_fault = client.post(
            f"/api/v1/practice-sessions/{session_id}/attempts",
            json={
                "instance_id": inst_0_id,
                "option_id": opt_0_id,
            },
            headers={
                "Idempotency-Key": str(uuid.uuid4()),
                "X-Test-Fault": "fail_after_attempt",
            },
        )
        assert resp_fault.status_code == 500, f"Expected 500 on injected fault, got {resp_fault.status_code}"

        # 4. Verify complete database rollback
        db_file = data_dir / "app.db"
        conn = sqlite3.connect(str(db_file))
        cur = conn.cursor()

        cur.execute("SELECT count(*) FROM attempt_event")
        assert cur.fetchone()[0] == 0, "All attempts must be rolled back on transaction failure"

        cur.execute("SELECT count(*) FROM review_event")
        assert cur.fetchone()[0] == 0, "All review events must be rolled back on failure"

        cur.execute("SELECT count(*) FROM exposure_event WHERE kind = 'feedback_available'")
        assert cur.fetchone()[0] == 0, "Feedback exposures must be rolled back on failure"

        cur.execute("SELECT cursor, revision FROM session WHERE id = ?", (session_id,))
        cur_row = cur.fetchone()
        assert cur_row[0] == 0, f"Session cursor should remain 0, got {cur_row[0]}"
        assert cur_row[1] == 0, f"Session revision should remain 0, got {cur_row[1]}"

        cur.execute("SELECT count(*) FROM correction_item")
        assert cur.fetchone()[0] == 0, "Correction items must be rolled back on failure"

        # 5. Normal submit without test fault should succeed cleanly
        resp_normal = client.post(
            f"/api/v1/practice-sessions/{session_id}/attempts",
            json={
                "instance_id": inst_0_id,
                "option_id": opt_0_id,
            },
            headers={
                "Idempotency-Key": str(uuid.uuid4()),
            },
        )
        assert resp_normal.status_code == 200
        fb_normal = resp_normal.json()
        assert fb_normal["next_cursor"] == 1
        rev_after_normal = fb_normal["session_revision"]

        cur.execute("SELECT count(*) FROM attempt_event")
        assert cur.fetchone()[0] == 1, "Exactly 1 attempt recorded after normal submission"

        # 6. End session
        resp_end = client.post(
            f"/api/v1/practice-sessions/{session_id}/end",
            json={
                "expected_revision": rev_after_normal,
                "reason": "ended",
            },
        )
        assert resp_end.status_code == 200
        assert resp_end.json()["session"]["status"] == "ended"

        # 7. Attempting next on closed session must return 409 SESSION_CLOSED
        resp_next_closed = client.post(
            f"/api/v1/practice-sessions/{session_id}/next",
            json={"expected_cursor": 1},
        )
        assert resp_next_closed.status_code == 409
        err_closed = resp_next_closed.json()["error"]
        assert err_closed["code"] == "SESSION_CLOSED"
        assert err_closed["details"]["session_id"] == session_id

        # Verify no illegal question instance was created
        cur.execute("SELECT count(*) FROM question_instance WHERE session_id = ?", (session_id,))
        assert cur.fetchone()[0] == 1, "Closed session must not create or retain new instances"

        # 8. Attempting to submit on closed session must return 409 SESSION_CLOSED
        resp_sub_closed = client.post(
            f"/api/v1/practice-sessions/{session_id}/attempts",
            json={
                "instance_id": inst_0_id,
                "option_id": opt_0_id,
            },
            headers={"Idempotency-Key": str(uuid.uuid4())},
        )
        assert resp_sub_closed.status_code == 409
        assert resp_sub_closed.json()["error"]["code"] == "SESSION_CLOSED"

        # 9. Creating a new session succeeds now that the previous session is ended
        resp_new = client.post(
            "/api/v1/practice-sessions",
            json={"bank_id": bank_id},
            headers={"Idempotency-Key": str(uuid.uuid4())},
        )
        assert resp_new.status_code == 201

        conn.close()
        client.close()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "transaction_rollback_verified": True,
            "session_closed_blocks_next_and_submit": True,
            "no_illegal_instances_retained": True,
            "open_session_slot_freed_on_end": True,
        },
    }
