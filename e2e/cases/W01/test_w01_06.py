"""W01-06: 展示不改变 attempt/review/cursor/normal_position/冷却/投影"""
import sqlite3
import uuid
from support.http_client import APIClient
from support.web_harness import WebHarness


def run(context: dict) -> dict:
    with WebHarness(context, seed="core") as harness:
        bank_id = "00000000-0000-4000-8000-000000000801"
        db_path = harness.data_dir / "app.db"
        client = APIClient(harness.base_url)
        client.initialize_client_context()

        try:
            # 1. Create session and answer Q0
            create_resp = client.post("/api/v1/practice-sessions", json={"bank_id": bank_id})
            assert create_resp.status_code == 201
            session_id = create_resp.json()["session"]["id"]

            next_resp = client.post(f"/api/v1/practice-sessions/{session_id}/next", json={"expected_cursor": 0})
            assert next_resp.status_code == 200
            q0 = next_resp.json()["question"]
            inst_id_0 = q0["instance_id"]

            att_resp = client.post(
                f"/api/v1/practice-sessions/{session_id}/attempts",
                json={"instance_id": inst_id_0, "option_id": q0["options"][0]["id"]},
            )
            assert att_resp.status_code == 200

            # 2. Capture baseline database state
            def capture_snapshot():
                with sqlite3.connect(f"file:{db_path}?mode=ro", uri=True) as conn:
                    cursor = conn.cursor()
                    cursor.execute("SELECT cursor, revision, normal_position FROM session WHERE id = ?", (session_id,))
                    sess_row = cursor.fetchone()

                    cursor.execute("SELECT COUNT(*), max(id) FROM attempt_event WHERE session_id = ?", (session_id,))
                    att_row = cursor.fetchone()

                    cursor.execute("SELECT COUNT(*) FROM review_event")
                    rev_count = cursor.fetchone()[0]

                    cursor.execute("SELECT COUNT(*) FROM exposure_event")
                    exp_count = cursor.fetchone()[0]

                    cursor.execute("SELECT COUNT(*) FROM evidence_projection")
                    ev_count = cursor.fetchone()[0]

                    cursor.execute("SELECT COUNT(*) FROM memory_projection")
                    mem_count = cursor.fetchone()[0]

                    cursor.execute("SELECT COUNT(*) FROM correction_item")
                    corr_count = cursor.fetchone()[0]

                    return {
                        "session": sess_row,
                        "attempt": att_row,
                        "review_count": rev_count,
                        "exposure_count": exp_count,
                        "evidence_count": ev_count,
                        "memory_count": mem_count,
                        "correction_count": corr_count,
                    }

            baseline = capture_snapshot()

            # 3. Post multiple feedback-view events
            for i in range(5):
                ev_id = str(uuid.uuid4())
                resp = client.post(
                    f"/api/v1/practice-sessions/{session_id}/feedback-views",
                    json={
                        "client_event_id": ev_id,
                        "instance_id": inst_id_0,
                        "presentation": "history" if i % 2 == 0 else "restored_feedback",
                        "client_displayed_at": f"2026-09-12T04:00:0{i}.000Z",
                    },
                )
                assert resp.status_code == 201, f"Failed post feedback view {i}: {resp.text}"

            # 4. Capture after state
            after = capture_snapshot()

            # 5. Assert strict invariance
            assert after["session"] == baseline["session"], f"Session changed: baseline={baseline['session']}, after={after['session']}"
            assert after["attempt"] == baseline["attempt"], f"Attempt changed: baseline={baseline['attempt']}, after={after['attempt']}"
            assert after["review_count"] == baseline["review_count"], f"Review count changed: {baseline['review_count']} -> {after['review_count']}"
            assert after["exposure_count"] == baseline["exposure_count"], f"Exposure count changed: {baseline['exposure_count']} -> {after['exposure_count']}"
            assert after["evidence_count"] == baseline["evidence_count"], f"Evidence projection changed: {baseline['evidence_count']} -> {after['evidence_count']}"
            assert after["memory_count"] == baseline["memory_count"], f"Memory projection changed: {baseline['memory_count']} -> {after['memory_count']}"
            assert after["correction_count"] == baseline["correction_count"], f"Correction count changed: {baseline['correction_count']} -> {after['correction_count']}"

            # 6. Verify practice_feedback_view table has 5 rows
            with sqlite3.connect(f"file:{db_path}?mode=ro", uri=True) as conn:
                cursor = conn.cursor()
                cursor.execute("SELECT COUNT(*) FROM practice_feedback_view WHERE session_id = ?", (session_id,))
                fv_count = cursor.fetchone()[0]
                assert fv_count == 5, f"Expected 5 feedback view rows, got {fv_count}"
        finally:
            client.close()

        return {
            "status": "passed",
            "details": {
                "invariance_verified": True,
                "feedback_views_recorded": 5,
            },
            "artifacts": harness.artifacts,
        }
