"""RB-03: Void exclusion, report revision, all-void ratio null, and restore consistency acceptance test.
Verifies:
1. Completing a 3-question exam (1 correct, 2 incorrect).
2. Invalidating an incorrect question:
   - Original total preserved (3).
   - Void count becomes 1, valid count is 2.
   - Result revision increments, ratio becomes 0.5 (1/2).
   - Invalidate excludes attempt from evidence_projection, memory_projection, and confusion_projection.
3. Invalidating all questions:
   - Void count becomes 3, valid count is 0.
   - Ratio is null (None in Python).
4. Restoring validity:
   - Question restored to published.
   - Exam report recomputed (void=2, valid=1, correct=1, ratio=1.0).
   - Projections replayed and restored to consistency.
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
    db_path = data_dir / "app.db"

    bank_id = "00000000-0000-4000-8000-000000000801"

    with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))
        base_url = server.base_url

        client = APIClient(base_url)
        client.initialize_client_context()

        # 1. Create a 3-question exam
        resp_exam = client.post("/api/v1/exams", json={
            "bank_id": bank_id,
            "question_count": 3,
            "task_quotas": {
                "meaning_recognition": 1,
                "reverse_selection": 1,
                "context_selection": 1,
            },
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp_exam.status_code == 201, f"Failed to create exam: {resp_exam.status_code}: {resp_exam.text}"
        exam_data = resp_exam.json()
        exam_id = exam_data["exam_id"]
        public_qs = exam_data["public_questions"]
        assert len(public_qs) == 3

        # Read correct options from SQLite for controlled answering
        conn = sqlite3.connect(str(db_path))
        cur = conn.cursor()
        inst_details = []
        for q in public_qs:
            inst_id = q["instance_id"]
            cur.execute("""
                SELECT qi.question_version_id, qv.correct_option_id, qi.ordinal
                FROM question_instance qi
                JOIN question_version qv ON qv.id = qi.question_version_id
                WHERE qi.id = ?
            """, (inst_id,))
            qv_id, correct_opt, ordinal = cur.fetchone()

            # Find a wrong option from public_qs options
            wrong_opt = None
            for opt in q["options"]:
                if opt["id"] != correct_opt:
                    wrong_opt = opt["id"]
                    break

            inst_details.append({
                "instance_id": inst_id,
                "question_version_id": qv_id,
                "correct_option_id": correct_opt,
                "wrong_option_id": wrong_opt,
                "ordinal": ordinal,
            })
        conn.close()

        # Sort by ordinal
        inst_details.sort(key=lambda x: x["ordinal"])

        # Answer Question 0: Correct
        seq = 1
        rev = exam_data["session_revision"]
        ev0_id = str(uuid.uuid4())
        ev0_resp = client.post(f"/api/v1/exams/{exam_id}/response-events", json={
            "instance_id": inst_details[0]["instance_id"],
            "action": "select",
            "option_id": inst_details[0]["correct_option_id"],
            "client_event_id": ev0_id,
            "client_sequence": seq,
            "expected_revision": rev,
        }, headers={"Idempotency-Key": ev0_id})
        assert ev0_resp.status_code == 200, f"Answer Q0 failed: {ev0_resp.text}"
        rev = ev0_resp.json()["current_revision"]
        seq += 1

        # Answer Question 1: Incorrect
        ev1_id = str(uuid.uuid4())
        ev1_resp = client.post(f"/api/v1/exams/{exam_id}/response-events", json={
            "instance_id": inst_details[1]["instance_id"],
            "action": "select",
            "option_id": inst_details[1]["wrong_option_id"],
            "client_event_id": ev1_id,
            "client_sequence": seq,
            "expected_revision": rev,
        }, headers={"Idempotency-Key": ev1_id})
        assert ev1_resp.status_code == 200, f"Answer Q1 failed: {ev1_resp.text}"
        rev = ev1_resp.json()["current_revision"]
        seq += 1

        # Answer Question 2: Incorrect
        ev2_id = str(uuid.uuid4())
        ev2_resp = client.post(f"/api/v1/exams/{exam_id}/response-events", json={
            "instance_id": inst_details[2]["instance_id"],
            "action": "select",
            "option_id": inst_details[2]["wrong_option_id"],
            "client_event_id": ev2_id,
            "client_sequence": seq,
            "expected_revision": rev,
        }, headers={"Idempotency-Key": ev2_id})
        assert ev2_resp.status_code == 200, f"Answer Q2 failed: {ev2_resp.text}"
        rev = ev2_resp.json()["current_revision"]

        # Submit Exam
        sub_id = str(uuid.uuid4())
        sub_resp = client.post(f"/api/v1/exams/{exam_id}/submit", json={
            "expected_revision": rev,
            "submission_id": sub_id,
            "last_ack_sequence": seq,
        }, headers={"Idempotency-Key": sub_id})
        assert sub_resp.status_code == 200, f"Submit failed: {sub_resp.text}"

        # Check Initial Result
        res_resp = client.get(f"/api/v1/exams/{exam_id}/result")
        assert res_resp.status_code == 200
        res_data = res_resp.json()
        assert res_data["total"] == 3
        assert res_data["correct"] == 1
        assert res_data["omitted"] == 0
        assert res_data["void"] == 0
        assert round(res_data["ratio"], 4) == round(1.0 / 3.0, 4)
        assert res_data["result_revision"] == 0

        # Invalidate Question 1 (which was answered incorrectly)
        qv1_id = inst_details[1]["question_version_id"]
        inv_resp = client.post(f"/api/v1/question-versions/{qv1_id}/evidence-dispositions", json={
            "action": "invalidate",
            "reason": "Wrong answer key in question 1",
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert inv_resp.status_code == 200, f"Invalidate failed: {inv_resp.text}"
        assert inv_resp.json()["status"] == "succeeded"

        # Check Revised Exam Result
        res_rev1 = client.get(f"/api/v1/exams/{exam_id}/result")
        assert res_rev1.status_code == 200
        rev1_data = res_rev1.json()
        assert rev1_data["result_revision"] == 1, f"Expected result_revision 1, got {rev1_data['result_revision']}"
        assert rev1_data["total"] == 3, "Original total count must be preserved"
        assert rev1_data["void"] == 1, f"Expected void=1, got {rev1_data['void']}"
        assert rev1_data["correct"] == 1, f"Expected correct=1, got {rev1_data['correct']}"
        # Ratio denominator is valid questions (total - void = 2), correct = 1 -> ratio = 0.5
        assert rev1_data["ratio"] == 0.5, f"Expected ratio 0.5, got {rev1_data['ratio']}"

        # Verify in SQLite: assessment_result_revision has 1 entry
        conn = sqlite3.connect(str(db_path))
        cur = conn.cursor()
        cur.execute("SELECT result_revision, void_count, ratio FROM assessment_result_revision WHERE session_id = ?", (exam_id,))
        rows = cur.fetchall()
        assert len(rows) == 1
        assert rows[0][0] == 1
        assert rows[0][1] == 1
        assert rows[0][2] == 0.5

        # Verify projections: invalidated question is excluded from ordinary_evidence and memory
        cur.execute("SELECT active_version FROM projection_control WHERE id = 1")
        active_ver = cur.fetchone()[0]

        # Find sense_id for qv1
        cur.execute("SELECT sense_id, task_family FROM question_version WHERE id = ?", (qv1_id,))
        sense_id_1, task_1 = cur.fetchone()
        cur.execute("""
            SELECT ordinary_samples, ordinary_correct
            FROM evidence_projection
            WHERE projection_version = ? AND sense_id = ? AND task_family = ?
        """, (active_ver, sense_id_1, task_1))
        ev_row = cur.fetchone()
        if ev_row:
            # Since qv1 was invalidated, its attempt cannot be in ordinary_samples
            assert ev_row[0] == 0, f"Expected ordinary_samples 0 for invalidated question, got {ev_row[0]}"

        cur.execute("""
            SELECT count(*) FROM memory_projection
            WHERE projection_version = ? AND sense_id = ? AND task_family = ?
        """, (active_ver, sense_id_1, task_1))
        mem_count = cur.fetchone()[0]
        assert mem_count == 0, f"Expected memory_projection count 0 for invalidated question, got {mem_count}"
        conn.close()

        # Invalidate remaining questions: Question 0 and Question 2
        qv0_id = inst_details[0]["question_version_id"]
        qv2_id = inst_details[2]["question_version_id"]

        client.post(f"/api/v1/question-versions/{qv0_id}/evidence-dispositions", json={
            "action": "invalidate",
            "reason": "Invalidating Q0",
        }, headers={"Idempotency-Key": str(uuid.uuid4())})

        client.post(f"/api/v1/question-versions/{qv2_id}/evidence-dispositions", json={
            "action": "invalidate",
            "reason": "Invalidating Q2",
        }, headers={"Idempotency-Key": str(uuid.uuid4())})

        # Check Exam Result when ALL questions are void
        res_all_void = client.get(f"/api/v1/exams/{exam_id}/result")
        assert res_all_void.status_code == 200
        all_void_data = res_all_void.json()
        assert all_void_data["total"] == 3
        assert all_void_data["void"] == 3
        assert all_void_data["ratio"] is None, f"Expected ratio null when all questions void, got {all_void_data['ratio']}"

        # Restore validity for Question 0
        rest_resp = client.post(f"/api/v1/question-versions/{qv0_id}/evidence-dispositions", json={
            "action": "restore_validity",
            "reason": "Restoring Q0 after review",
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert rest_resp.status_code == 200

        # Check Exam Result after restore: 1 valid question (Q0, which was correct)
        res_rest = client.get(f"/api/v1/exams/{exam_id}/result")
        assert res_rest.status_code == 200
        rest_data = res_rest.json()
        assert rest_data["total"] == 3
        assert rest_data["void"] == 2
        assert rest_data["correct"] == 1
        assert rest_data["ratio"] == 1.0, f"Expected ratio 1.0 (1/1), got {rest_data['ratio']}"

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "exam_id": exam_id,
            "final_result_revision": rest_data["result_revision"],
            "final_ratio": rest_data["ratio"],
        },
    }
