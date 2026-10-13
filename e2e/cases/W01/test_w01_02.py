"""W01-02: 原请求重放、响应丢失、换选项冲突"""
import sqlite3
from support.http_client import APIClient
from support.web_harness import WebHarness


def run(context: dict) -> dict:
    with WebHarness(context, seed="core") as harness:
        bank_id = "00000000-0000-4000-8000-000000000801"
        client = APIClient(harness.base_url)
        client.initialize_client_context()

        try:
            # 1. Create practice session
            create_resp = client.post("/api/v1/practice-sessions", json={"bank_id": bank_id})
            assert create_resp.status_code == 201
            session_id = create_resp.json()["session"]["id"]

            # 2. Get first question (ordinal 0)
            next_resp = client.post(f"/api/v1/practice-sessions/{session_id}/next", json={"expected_cursor": 0})
            assert next_resp.status_code == 200
            q0 = next_resp.json()["question"]
            inst_id_0 = q0["instance_id"]
            opts = q0["options"]
            assert len(opts) >= 2, "Question must have at least 2 options"
            opt_a = opts[0]["id"]
            opt_b = opts[1]["id"]

            # 3. Submit attempt with Idempotency-Key
            idemp_key = "idemp-attempt-test-w01-02"
            payload_a = {"instance_id": inst_id_0, "option_id": opt_a}
            att_resp = client.post(
                f"/api/v1/practice-sessions/{session_id}/attempts",
                json=payload_a,
                headers={"Idempotency-Key": idemp_key},
            )
            assert att_resp.status_code == 200, f"Attempt failed: {att_resp.text}"
            fb1 = att_resp.json()

            # 4. Replay identical request with same key (simulating response loss / network retry)
            replay_resp = client.post(
                f"/api/v1/practice-sessions/{session_id}/attempts",
                json=payload_a,
                headers={"Idempotency-Key": idemp_key},
            )
            assert replay_resp.status_code == 200, f"Replay failed: {replay_resp.text}"
            assert replay_resp.headers.get("Idempotent-Replayed") == "true", "Expected Idempotent-Replayed header"
            fb_replay = replay_resp.json()
            assert fb_replay["attempt_id"] == fb1["attempt_id"]
            assert fb_replay["correct"] == fb1["correct"]

            # 5. Send conflicting request with same key but changed option
            payload_b = {"instance_id": inst_id_0, "option_id": opt_b}
            conflict_resp = client.post(
                f"/api/v1/practice-sessions/{session_id}/attempts",
                json=payload_b,
                headers={"Idempotency-Key": idemp_key},
            )
            assert conflict_resp.status_code == 409, f"Expected 409 conflict, got {conflict_resp.status_code}"
            assert conflict_resp.json()["error"]["code"] == "IDEMPOTENCY_CONFLICT"

            # 6. Send request with new key trying to change answer on already-answered instance
            new_key = "idemp-attempt-new-key"
            already_ans_resp = client.post(
                f"/api/v1/practice-sessions/{session_id}/attempts",
                json=payload_b,
                headers={"Idempotency-Key": new_key},
            )
            assert already_ans_resp.status_code == 409, f"Expected 409 already answered, got {already_ans_resp.status_code}"
            assert already_ans_resp.json()["error"]["code"] == "ALREADY_ANSWERED"
        finally:
            client.close()

        # 7. Check SQLite state: verify exactly one attempt recorded with original choice
        db_path = harness.data_dir / "app.db"
        with sqlite3.connect(f"file:{db_path}?mode=ro", uri=True) as conn:
            cursor = conn.cursor()
            cursor.execute(
                "SELECT COUNT(*), selected_option_id FROM attempt_event WHERE instance_id = ? GROUP BY selected_option_id",
                (inst_id_0,),
            )
            rows = cursor.fetchall()
            assert len(rows) == 1, f"Expected exactly 1 attempt row, got {rows}"
            count, db_opt = rows[0]
            assert count == 1, f"Expected count=1, got {count}"
            assert db_opt == opt_a, f"Expected selected_option_id={opt_a}, got {db_opt}"

        return {
            "status": "passed",
            "details": {
                "idempotent_replay_verified": True,
                "digest_conflict_verified": True,
                "already_answered_lock_verified": True,
            },
            "artifacts": harness.artifacts,
        }
