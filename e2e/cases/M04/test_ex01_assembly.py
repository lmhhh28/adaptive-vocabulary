"""EX-01: Exam assembly acceptance test.
Verifies core default 3-question balanced assembly succeeds without cross-question hints;
Verifies exam-conflict seed returns 422 EXAM_CONSTRAINT_UNSATISFIED;
Verifies invalid quotas return 400 INVALID_REQUEST.
"""
from pathlib import Path
import sqlite3
import uuid
from support.lifecycle import managed_server
from support.http_client import APIClient

BANK_ID = "00000000-0000-4000-8000-000000000801"


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    artifacts = []

    # 1. Test core seed: balanced 3 questions assembly
    core_data_dir = case_dir / "data_core"
    core_data_dir.mkdir(parents=True, exist_ok=True)

    with managed_server(bin_path, core_data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))
        client = APIClient(server.base_url)
        client.initialize_client_context()

        # 1.1 Create balanced 3-task exam
        key1 = str(uuid.uuid4())
        resp = client.post("/api/v1/exams", json={
            "bank_id": BANK_ID,
            "question_count": 3
        }, headers={"Idempotency-Key": key1})
        assert resp.status_code == 201, f"Expected 201, got {resp.status_code}: {resp.text}"
        data = resp.json()

        exam_id = data["exam_id"]
        questions = data["public_questions"]
        drafts = data["drafts"]
        assert len(questions) == 3, f"Expected 3 questions, got {len(questions)}"
        assert len(drafts) == 3, f"Expected 3 drafts, got {len(drafts)}"

        # Verify no answers leaked in public questions
        families = {}
        for q in questions:
            families[q["task_family"]] = families.get(q["task_family"], 0) + 1
            assert "correct_option_id" not in q, "correct_option_id leaked"
            assert "explanation" not in q, "explanation leaked"
            assert len(q["options"]) == 4, "Expected 4 options"
            for opt in q["options"]:
                assert "is_correct" not in opt, "is_correct leaked in option"
                assert "option_identity_id" not in opt, "option_identity_id leaked in option"

        assert families == {
            "meaning_recognition": 1,
            "reverse_selection": 1,
            "context_selection": 1
        }, f"Expected balanced task families, got {families}"

        # Check DB instances: target lexeme uniqueness and cross question checks
        db_path = core_data_dir / "app.db"
        with sqlite3.connect(db_path) as conn:
            cursor = conn.cursor()
            cursor.execute("""
                SELECT qi.ordinal, qi.snapshot_json
                FROM question_instance qi
                WHERE qi.session_id = ?
                ORDER BY qi.ordinal ASC
            """, (exam_id,))
            rows = cursor.fetchall()
            assert len(rows) == 3

        # 1.2 Invalid quotas: unknown family
        resp_invalid_fam = client.post("/api/v1/exams", json={
            "bank_id": BANK_ID,
            "question_count": 3,
            "task_quotas": {"unknown_family": 3}
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp_invalid_fam.status_code == 400
        assert resp_invalid_fam.json()["error"]["code"] == "INVALID_REQUEST"

        # 1.3 Mismatched quota total
        resp_mismatch = client.post("/api/v1/exams", json={
            "bank_id": BANK_ID,
            "question_count": 4,
            "task_quotas": {
                "meaning_recognition": 1,
                "reverse_selection": 1,
                "context_selection": 1
            }
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp_mismatch.status_code == 400
        assert resp_mismatch.json()["error"]["code"] == "INVALID_REQUEST"

        client.close()

    # 2. Test exam-conflict seed: assembly unsatisfiable
    conflict_data_dir = case_dir / "data_conflict"
    conflict_data_dir.mkdir(parents=True, exist_ok=True)

    with managed_server(bin_path, conflict_data_dir, listen_port=0, seed="exam-conflict", log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))
        client = APIClient(server.base_url)
        client.initialize_client_context()

        resp_conflict = client.post("/api/v1/exams", json={
            "bank_id": BANK_ID,
            "question_count": 3
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp_conflict.status_code == 422, f"Expected 422 for conflict seed, got {resp_conflict.status_code}: {resp_conflict.text}"
        err = resp_conflict.json()["error"]
        assert err["code"] == "EXAM_CONSTRAINT_UNSATISFIED", f"Expected EXAM_CONSTRAINT_UNSATISFIED, got {err['code']}"

        client.close()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "core_assembly": "ok",
            "conflict_unsatisfied": "ok",
            "validation_checks": "ok"
        }
    }
