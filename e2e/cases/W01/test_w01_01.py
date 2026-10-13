"""W01-01: 已答未 next 的完整反馈恢复"""
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
            assert create_resp.status_code == 201, f"Failed to create session: {create_resp.text}"
            session_id = create_resp.json()["session"]["id"]

            # 2. Get first question (ordinal 0)
            next_resp = client.post(f"/api/v1/practice-sessions/{session_id}/next", json={"expected_cursor": 0})
            assert next_resp.status_code == 200, f"Failed next: {next_resp.text}"
            next_data = next_resp.json()
            assert next_data["status"] == "ready"
            q0 = next_data["question"]
            inst_id_0 = q0["instance_id"]
            assert q0["ordinal"] == 0

            # 3. Check session detail before answering
            det0_resp = client.get(f"/api/v1/practice-sessions/{session_id}")
            assert det0_resp.status_code == 200
            det0 = det0_resp.json()
            assert det0.get("current_question") is not None, "Expected current_question before answering"
            assert det0["current_question"]["instance_id"] == inst_id_0
            assert det0.get("last_answered_question") is None, "Expected last_answered_question=None"
            assert det0["summary"]["answered_count"] == 0
            assert len(det0["recent_records"]) == 0

            # 4. Submit attempt for question 0
            selected_opt = q0["options"][0]["id"]
            att_resp = client.post(
                f"/api/v1/practice-sessions/{session_id}/attempts",
                json={"instance_id": inst_id_0, "option_id": selected_opt},
                headers={"Idempotency-Key": "att-key-0"},
            )
            assert att_resp.status_code == 200, f"Failed attempt: {att_resp.text}"
            fb = att_resp.json()
            assert fb["next_cursor"] == 1

            # 5. In answered-but-not-next state, check recovery via GET session detail
            det1_resp = client.get(f"/api/v1/practice-sessions/{session_id}")
            assert det1_resp.status_code == 200
            assert "no-store" in det1_resp.headers.get("cache-control", "").lower()
            det1 = det1_resp.json()

            assert det1.get("current_question") is None, "current_question must be None when answered but not next"
            last_ans = det1.get("last_answered_question")
            assert last_ans is not None, "last_answered_question must be populated for feedback recovery"
            assert last_ans["question"]["instance_id"] == inst_id_0
            assert last_ans["question"]["ordinal"] == 0
            assert last_ans["selected_option_id"] == selected_opt
            assert last_ans["feedback"]["attempt_id"] == fb["attempt_id"]
            assert last_ans["feedback"]["correct"] == fb["correct"]
            assert last_ans["feedback"]["correct_option_id"] == fb["correct_option_id"]
            assert last_ans["feedback"]["explanation"] == fb["explanation"]

            assert det1["summary"]["counts_scope"] == "accepted_attempts"
            assert det1["summary"]["answered_count"] == 1
            if fb["correct"]:
                assert det1["summary"]["correct_count"] == 1
                assert det1["summary"]["incorrect_count"] == 0
            else:
                assert det1["summary"]["correct_count"] == 0
                assert det1["summary"]["incorrect_count"] == 1

            assert len(det1["recent_records"]) == 1
            assert det1["recent_records"][0]["instance_id"] == inst_id_0
            assert det1["recent_records"][0]["ordinal"] == 0
            assert det1["has_older_records"] is False

            # 6. Call /next to advance to question 1
            next1_resp = client.post(f"/api/v1/practice-sessions/{session_id}/next", json={"expected_cursor": 1})
            assert next1_resp.status_code == 200
            q1 = next1_resp.json()["question"]
            assert q1["ordinal"] == 1

            # 7. Check session detail after next: current_question present, last_answered_question hidden
            det2_resp = client.get(f"/api/v1/practice-sessions/{session_id}")
            assert det2_resp.status_code == 200
            det2 = det2_resp.json()
            assert det2.get("current_question") is not None
            assert det2["current_question"]["ordinal"] == 1
            assert det2.get("last_answered_question") is None, "Old answer must not leak when answering new question"
            assert det2["summary"]["answered_count"] == 1
        finally:
            client.close()

        return {
            "status": "passed",
            "details": {
                "session_id": session_id,
                "feedback_recovered": True,
                "old_answer_protected": True,
            },
            "artifacts": harness.artifacts,
        }
