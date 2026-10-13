"""W01-03: 超过30条题卡分页、计数和 ordinal=0"""
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

            # 2. Answer 35 questions (ordinals 0 to 34)
            total_questions = 35
            for ordinal in range(total_questions):
                next_resp = client.post(
                    f"/api/v1/practice-sessions/{session_id}/next",
                    json={"expected_cursor": ordinal},
                )
                assert next_resp.status_code == 200, f"Failed next at ordinal {ordinal}: {next_resp.text}"
                q = next_resp.json()["question"]
                assert q["ordinal"] == ordinal

                opt_id = q["options"][0]["id"]
                att_resp = client.post(
                    f"/api/v1/practice-sessions/{session_id}/attempts",
                    json={"instance_id": q["instance_id"], "option_id": opt_id},
                    headers={"Idempotency-Key": f"key-{session_id}-{ordinal}"},
                )
                assert att_resp.status_code == 200, f"Failed attempt at ordinal {ordinal}: {att_resp.text}"

            # 3. Verify session detail contains 30 recent records and has_older_records is True
            det_resp = client.get(f"/api/v1/practice-sessions/{session_id}")
            assert det_resp.status_code == 200
            det = det_resp.json()
            assert det["summary"]["answered_count"] == 35
            assert len(det["recent_records"]) == 30
            assert det["has_older_records"] is True
            recent_ordinals = [r["ordinal"] for r in det["recent_records"]]
            expected_recent = list(range(5, 35))
            assert recent_ordinals == expected_recent, f"Expected {expected_recent}, got {recent_ordinals}"

            # 4. Default answer-records page
            rec_resp = client.get(f"/api/v1/practice-sessions/{session_id}/answer-records")
            assert rec_resp.status_code == 200
            page1 = rec_resp.json()
            assert len(page1["items"]) == 30
            assert [r["ordinal"] for r in page1["items"]] == expected_recent
            assert page1["has_more"] is True
            assert page1["next_before_ordinal"] == 5
            assert page1["summary"]["answered_count"] == 35

            # 5. Fetch previous page using before_ordinal=5
            rec2_resp = client.get(f"/api/v1/practice-sessions/{session_id}/answer-records?before_ordinal=5")
            assert rec2_resp.status_code == 200
            page2 = rec2_resp.json()
            assert len(page2["items"]) == 5
            assert [r["ordinal"] for r in page2["items"]] == list(range(0, 5))
            assert page2["has_more"] is False
            assert page2["next_before_ordinal"] is None

            # 6. Test before_ordinal=0 (must not fail or be ignored due to falsy check)
            rec0_resp = client.get(f"/api/v1/practice-sessions/{session_id}/answer-records?before_ordinal=0")
            assert rec0_resp.status_code == 200
            page0 = rec0_resp.json()
            assert len(page0["items"]) == 0
            assert page0["has_more"] is False
            assert page0["next_before_ordinal"] is None

            # 7. Test custom limit: limit=10
            limit_resp = client.get(f"/api/v1/practice-sessions/{session_id}/answer-records?limit=10")
            assert limit_resp.status_code == 200
            page_lim = limit_resp.json()
            assert len(page_lim["items"]) == 10
            assert [r["ordinal"] for r in page_lim["items"]] == list(range(25, 35))
            assert page_lim["has_more"] is True
            assert page_lim["next_before_ordinal"] == 25

            # 8. Test invalid limit values
            inv1 = client.get(f"/api/v1/practice-sessions/{session_id}/answer-records?limit=0")
            assert inv1.status_code == 400
            inv2 = client.get(f"/api/v1/practice-sessions/{session_id}/answer-records?limit=101")
            assert inv2.status_code == 400
        finally:
            client.close()

        return {
            "status": "passed",
            "details": {
                "total_answered": total_questions,
                "recent_records_count": len(recent_ordinals),
                "pagination_before_5_verified": True,
                "before_ordinal_0_verified": True,
            },
            "artifacts": harness.artifacts,
        }
