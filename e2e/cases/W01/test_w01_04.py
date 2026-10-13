"""W01-04: 未答、未来题、跨会话读取禁止泄题"""
from support.http_client import APIClient
from support.web_harness import WebHarness


def run(context: dict) -> dict:
    with WebHarness(context, seed="core") as harness:
        bank_id = "00000000-0000-4000-8000-000000000801"
        client = APIClient(harness.base_url)
        client.initialize_client_context()

        try:
            # 1. Create practice session A
            create_a = client.post("/api/v1/practice-sessions", json={"bank_id": bank_id})
            assert create_a.status_code == 201
            sess_a = create_a.json()["session"]["id"]

            # 2. Get Q0 and answer it
            n0 = client.post(f"/api/v1/practice-sessions/{sess_a}/next", json={"expected_cursor": 0})
            assert n0.status_code == 200
            q0 = n0.json()["question"]
            att0 = client.post(
                f"/api/v1/practice-sessions/{sess_a}/attempts",
                json={"instance_id": q0["instance_id"], "option_id": q0["options"][0]["id"]},
            )
            assert att0.status_code == 200

            # 3. Get Q1 and DO NOT answer it
            n1 = client.post(f"/api/v1/practice-sessions/{sess_a}/next", json={"expected_cursor": 1})
            assert n1.status_code == 200

            # 4. Attempt to read answered question for unanswered Q1 -> 404
            unanswered_resp = client.get(f"/api/v1/practice-sessions/{sess_a}/answered-questions/1")
            assert unanswered_resp.status_code == 404, f"Expected 404 for unanswered, got {unanswered_resp.status_code}"
            assert unanswered_resp.json()["error"]["code"] == "NOT_FOUND"

            # 5. Attempt to read answered question for future questions -> 404
            fut_resp = client.get(f"/api/v1/practice-sessions/{sess_a}/answered-questions/2")
            assert fut_resp.status_code == 404
            fut99_resp = client.get(f"/api/v1/practice-sessions/{sess_a}/answered-questions/99")
            assert fut99_resp.status_code == 404

            # 6. Read answered question for Q0 in the same session -> 200
            ans0_resp = client.get(f"/api/v1/practice-sessions/{sess_a}/answered-questions/0")
            assert ans0_resp.status_code == 200
            ans0 = ans0_resp.json()
            assert ans0["question"]["ordinal"] == 0
            assert "feedback" in ans0

            # 7. End session A
            det_a = client.get(f"/api/v1/practice-sessions/{sess_a}").json()
            rev_a = det_a["session"]["revision"]
            end_a = client.post(
                f"/api/v1/practice-sessions/{sess_a}/end",
                json={"expected_revision": rev_a, "reason": "ended"},
            )
            assert end_a.status_code == 200

            # 8. Create session B (now active)
            create_b = client.post("/api/v1/practice-sessions", json={"bank_id": bank_id})
            assert create_b.status_code == 201
            sess_b = create_b.json()["session"]["id"]
            assert sess_b != sess_a

            # 9. While session B is active, attempt to read answered questions from session A
            # Forbidden to prevent answer leakage!
            leak_resp = client.get(f"/api/v1/practice-sessions/{sess_a}/answered-questions/0")
            assert leak_resp.status_code == 409, f"Expected 409 conflict during active session, got {leak_resp.status_code}"
            assert leak_resp.json()["error"]["code"] == "SESSION_ACTIVE"

            # 10. End session B
            det_b = client.get(f"/api/v1/practice-sessions/{sess_b}").json()
            rev_b = det_b["session"]["revision"]
            end_b = client.post(
                f"/api/v1/practice-sessions/{sess_b}/end",
                json={"expected_revision": rev_b, "reason": "ended"},
            )
            assert end_b.status_code == 200

            # 11. Now that session B has ended, reading historical answered questions from session A is allowed
            hist_resp = client.get(f"/api/v1/practice-sessions/{sess_a}/answered-questions/0")
            assert hist_resp.status_code == 200
            assert hist_resp.json()["question"]["ordinal"] == 0
        finally:
            client.close()

        return {
            "status": "passed",
            "details": {
                "unanswered_ordinal_rejected": True,
                "future_ordinal_rejected": True,
                "cross_session_active_protection_verified": True,
                "inactive_history_allowed": True,
            },
            "artifacts": harness.artifacts,
        }
