"""EX-03: Exam scoring, response history classifications and FSRS observations.
Verifies:
- First-wrong then right (revised to correct);
- Single reliable choice updates FSRS;
- Revised and omitted attempts do NOT update FSRS;
- First choice accuracy analysis;
- Exposure events feedback_available generated on submission.
"""
import json
from pathlib import Path
import sqlite3
import uuid
from support.lifecycle import managed_server
from support.http_client import APIClient

BANK_ID = "00000000-0000-4000-8000-000000000801"


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    data_dir = case_dir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)
    artifacts = []

    with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))
        client = APIClient(server.base_url)
        client.initialize_client_context()

        # 1. Create exam
        resp = client.post("/api/v1/exams", json={
            "bank_id": BANK_ID,
            "question_count": 3
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp.status_code == 201
        data = resp.json()
        exam_id = data["exam_id"]
        q0 = data["public_questions"][0]
        q1 = data["public_questions"][1]
        q2 = data["public_questions"][2]

        # Inspect DB to know the real correct_option_id for each question
        db_path = data_dir / "app.db"
        correct_map = {}
        with sqlite3.connect(db_path) as conn:
            c = conn.cursor()
            c.execute("SELECT id, snapshot_json FROM question_instance WHERE session_id = ?", (exam_id,))
            for i_id, snap_str in c.fetchall():
                snap = json.loads(snap_str)
                correct_map[i_id] = snap["correct_option_id"]

        q0_correct = correct_map[q0["instance_id"]]
        q0_wrong = [opt["id"] for opt in q0["options"] if opt["id"] != q0_correct][0]

        q1_correct = correct_map[q1["instance_id"]]

        # 2. Sequence of events
        # Question 0: First select wrong option
        ev1_id = str(uuid.uuid4())
        r1 = client.post(f"/api/v1/exams/{exam_id}/response-events", json={
            "instance_id": q0["instance_id"],
            "action": "select",
            "option_id": q0_wrong,
            "client_event_id": ev1_id,
            "client_sequence": 1,
            "expected_revision": 0
        }, headers={"Idempotency-Key": ev1_id})
        assert r1.status_code == 200

        # Question 0: Noop (select same wrong option again) -> doesn't increase revision_count on draft
        ev2_id = str(uuid.uuid4())
        r2 = client.post(f"/api/v1/exams/{exam_id}/response-events", json={
            "instance_id": q0["instance_id"],
            "action": "select",
            "option_id": q0_wrong,
            "client_event_id": ev2_id,
            "client_sequence": 2,
            "expected_revision": 1
        }, headers={"Idempotency-Key": ev2_id})
        assert r2.status_code == 200

        # Question 0: Revise to correct option!
        ev3_id = str(uuid.uuid4())
        r3 = client.post(f"/api/v1/exams/{exam_id}/response-events", json={
            "instance_id": q0["instance_id"],
            "action": "select",
            "option_id": q0_correct,
            "client_event_id": ev3_id,
            "client_sequence": 3,
            "expected_revision": 2
        }, headers={"Idempotency-Key": ev3_id})
        assert r3.status_code == 200
        assert r3.json()["draft"]["revision_count"] == 1

        # Question 1: Single reliable choice -> correct option
        ev4_id = str(uuid.uuid4())
        r4 = client.post(f"/api/v1/exams/{exam_id}/response-events", json={
            "instance_id": q1["instance_id"],
            "action": "select",
            "option_id": q1_correct,
            "client_event_id": ev4_id,
            "client_sequence": 4,
            "expected_revision": 3
        }, headers={"Idempotency-Key": ev4_id})
        assert r4.status_code == 200
        assert r4.json()["draft"]["revision_count"] == 0

        # Question 2: Left omitted (no events)

        # 3. Submit exam
        sub_id = str(uuid.uuid4())
        sub_resp = client.post(f"/api/v1/exams/{exam_id}/submit", json={
            "expected_revision": 4,
            "submission_id": sub_id,
            "last_ack_sequence": 4
        }, headers={"Idempotency-Key": sub_id})
        assert sub_resp.status_code == 200, f"Submit failed: {sub_resp.text}"

        # 4. Query Exam Result Report
        res_resp = client.get(f"/api/v1/exams/{exam_id}/result")
        assert res_resp.status_code == 200
        result = res_resp.json()

        assert result["total"] == 3
        assert result["correct"] == 2
        assert result["omitted"] == 1
        assert abs(result["ratio"] - (2.0 / 3.0)) < 1e-4

        analysis = result["response_analysis"]
        assert analysis["first_choice_correct_count"] == 1, "Question 1 was first choice correct"
        assert analysis["revised_to_correct_count"] == 1, "Question 0 was revised to correct"
        assert analysis["reliable_first_choices"] == 2

        # 5. Verify database records
        with sqlite3.connect(db_path) as conn:
            c = conn.cursor()

            # Attempt events: 3 attempts created
            c.execute("""
                SELECT instance_id, response_history, is_correct, revision_count
                FROM attempt_event
                WHERE session_id = ?
                ORDER BY instance_id ASC
            """, (exam_id,))
            attempts = c.fetchall()
            assert len(attempts) == 3

            hist_map = {row[0]: row[1] for row in attempts}
            assert hist_map[q0["instance_id"]] == "revised"
            assert hist_map[q1["instance_id"]] == "single_response"
            assert hist_map[q2["instance_id"]] == "omitted"

            # FSRS review events: ONLY single_response creates a review_event!
            c.execute("""
                SELECT re.id, ae.instance_id, re.rating, re.stability, re.difficulty
                FROM review_event re
                JOIN attempt_event ae ON ae.id = re.attempt_id
                WHERE ae.session_id = ?
            """, (exam_id,))
            reviews = c.fetchall()
            assert len(reviews) == 1, f"Expected exactly 1 review_event (for single_response), got {len(reviews)}"
            assert reviews[0][1] == q1["instance_id"], "Review event must be for question 1"

            # Exposure events: 3 reserved + 3 feedback_available
            c.execute("""
                SELECT kind, count(*)
                FROM exposure_event ee
                JOIN question_instance qi ON qi.id = ee.source_instance_id
                WHERE qi.session_id = ?
                GROUP BY kind
            """, (exam_id,))
            exp_counts = dict(c.fetchall())
            assert exp_counts.get("reserved") == 3
            assert exp_counts.get("feedback_available") == 3

        client.close()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "scoring": "ok",
            "response_histories": "ok",
            "fsrs_isolation": "ok",
            "exposures": "ok"
        }
    }
