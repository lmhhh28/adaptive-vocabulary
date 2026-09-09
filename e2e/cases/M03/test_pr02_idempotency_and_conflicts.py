"""PR-02: Idempotency, replay, and conflict acceptance test.
Verifies response loss retry (same key) -> different key same option replay ->
different option conflict (ALREADY_ANSWERED) -> cursor mismatch (REVISION_CONFLICT).
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
        assert resp.status_code == 201, f"Expected 201 created, got {resp.status_code}: {resp.text}"
        session_id = resp.json()["session"]["id"]

        # 2. Get first question (cursor 0)
        resp_next = client.post(f"/api/v1/practice-sessions/{session_id}/next", json={"expected_cursor": 0})
        assert resp_next.status_code == 200, f"Expected 200 from next, got {resp_next.status_code}"
        next_data = resp_next.json()
        assert next_data["status"] == "ready"
        q = next_data["question"]
        inst_id = q["instance_id"]
        opt_a = q["options"][0]["id"]
        opt_b = q["options"][1]["id"]

        # 3. First submission with Idempotency-Key KEY-1
        key_1 = str(uuid.uuid4())
        resp_sub1 = client.post(f"/api/v1/practice-sessions/{session_id}/attempts", json={
            "instance_id": inst_id,
            "option_id": opt_a,
        }, headers={"Idempotency-Key": key_1})
        assert resp_sub1.status_code == 200, f"Expected 200 on first submit, got {resp_sub1.status_code}: {resp_sub1.text}"
        fb1 = resp_sub1.json()
        assert fb1["next_cursor"] == 1
        att_id_1 = fb1["attempt_id"]

        # 4. Retry with SAME key KEY-1 (simulating lost response)
        resp_sub1_retry = client.post(f"/api/v1/practice-sessions/{session_id}/attempts", json={
            "instance_id": inst_id,
            "option_id": opt_a,
        }, headers={"Idempotency-Key": key_1})
        assert resp_sub1_retry.status_code == 200
        assert resp_sub1_retry.headers.get("Idempotent-Replayed") == "true" or resp_sub1_retry.headers.get("idempotent-replayed") == "true"
        fb1_retry = resp_sub1_retry.json()
        assert fb1_retry["attempt_id"] == att_id_1, "Expected identical attempt_id on idempotent retry"

        # Check DB count: exactly 1 attempt_event
        db_file = data_dir / "app.db"
        conn = sqlite3.connect(str(db_file))
        cur = conn.cursor()

        cur.execute("SELECT count(*) FROM attempt_event WHERE instance_id = ?", (inst_id,))
        assert cur.fetchone()[0] == 1, "Expected exactly 1 attempt_event in DB"

        cur.execute("SELECT count(*) FROM review_event WHERE attempt_id = ?", (att_id_1,))
        assert cur.fetchone()[0] == 1, "Expected exactly 1 review_event in DB"

        cur.execute("SELECT count(*) FROM exposure_event WHERE source_instance_id = ? AND kind = 'feedback_available'", (inst_id,))
        assert cur.fetchone()[0] == 1, "Expected exactly 1 feedback_available exposure in DB"

        # 5. Resend with DIFFERENT key KEY-2, but SAME option_id
        key_2 = str(uuid.uuid4())
        resp_sub2 = client.post(f"/api/v1/practice-sessions/{session_id}/attempts", json={
            "instance_id": inst_id,
            "option_id": opt_a,
        }, headers={"Idempotency-Key": key_2})
        assert resp_sub2.status_code == 200, f"Expected 200 on same option repeat, got {resp_sub2.status_code}"
        fb2 = resp_sub2.json()
        assert fb2["attempt_id"] == att_id_1, "Expected same attempt_id when resubmitting same option"

        # DB still has only 1 attempt
        cur.execute("SELECT count(*) FROM attempt_event WHERE instance_id = ?", (inst_id,))
        assert cur.fetchone()[0] == 1, "Expected still 1 attempt_event after resubmitting same option"

        # 6. Submit with DIFFERENT option_id opt_b for same instance -> ALREADY_ANSWERED (409)
        key_3 = str(uuid.uuid4())
        resp_sub3 = client.post(f"/api/v1/practice-sessions/{session_id}/attempts", json={
            "instance_id": inst_id,
            "option_id": opt_b,
        }, headers={"Idempotency-Key": key_3})
        assert resp_sub3.status_code == 409, f"Expected 409 on different option submit, got {resp_sub3.status_code}"
        err_data = resp_sub3.json()
        assert err_data["error"]["code"] == "ALREADY_ANSWERED"
        assert "session_id" in err_data["error"]["details"]
        assert "cursor" in err_data["error"]["details"]

        # 7. Test cursor conflict on next (e.g. expected_cursor: 99 when actual is 1)
        resp_bad_cursor = client.post(f"/api/v1/practice-sessions/{session_id}/next", json={"expected_cursor": 99})
        assert resp_bad_cursor.status_code == 409
        err_cursor = resp_bad_cursor.json()
        assert err_cursor["error"]["code"] == "REVISION_CONFLICT"
        assert err_cursor["error"]["details"]["cursor"] == 1

        conn.close()
        client.close()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "idempotent_replay_verified": True,
            "same_option_replayed_without_duplicates": True,
            "different_option_rejected_as_already_answered": True,
            "cursor_conflict_detected": True,
        },
    }
