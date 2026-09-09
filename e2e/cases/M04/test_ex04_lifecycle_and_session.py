"""EX-04: Exam lifecycle, deadline settlement, recovery and single active slot constraint.
Verifies:
- Cross-mode session concurrency exclusivity (practice vs exam);
- Reload / recovery restores drafts;
- Restart settles expired exams and frees the active slot;
- Post-submit events rejected.
"""
from pathlib import Path
import sqlite3
import time
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

    # 1. Start server, create exam
    with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))
        client = APIClient(server.base_url)
        client.initialize_client_context()

        # 1.1 Create open exam
        resp_create = client.post("/api/v1/exams", json={
            "bank_id": BANK_ID,
            "question_count": 3,
            "time_limit_seconds": 300
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp_create.status_code == 201
        exam_id = resp_create.json()["exam_id"]
        q0 = resp_create.json()["public_questions"][0]
        opt0 = q0["options"][0]["id"]

        # 1.2 Cross-mode exclusivity: cannot create practice session while exam is open
        resp_practice = client.post("/api/v1/practice-sessions", json={
            "bank_id": BANK_ID
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp_practice.status_code == 409, f"Expected 409, got {resp_practice.status_code}: {resp_practice.text}"
        assert resp_practice.json()["error"]["code"] == "SESSION_ACTIVE"

        # 1.3 Cannot create second exam while one is open
        resp_exam2 = client.post("/api/v1/exams", json={
            "bank_id": BANK_ID,
            "question_count": 3
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp_exam2.status_code == 409
        assert resp_exam2.json()["error"]["code"] == "SESSION_ACTIVE"

        # 1.4 Select an option on q0
        ev_id = str(uuid.uuid4())
        r_ev = client.post(f"/api/v1/exams/{exam_id}/response-events", json={
            "instance_id": q0["instance_id"],
            "action": "select",
            "option_id": opt0,
            "client_event_id": ev_id,
            "client_sequence": 1,
            "expected_revision": 0
        }, headers={"Idempotency-Key": ev_id})
        assert r_ev.status_code == 200

        # 1.5 Recovery check: GET /api/v1/exams/{id} returns saved draft
        r_get = client.get(f"/api/v1/exams/{exam_id}")
        assert r_get.status_code == 200
        exam_detail = r_get.json()
        assert exam_detail["status"] == "open"
        assert exam_detail["session_revision"] == 1
        d0 = next(d for d in exam_detail["drafts"] if d["instance_id"] == q0["instance_id"])
        assert d0["selected_option_id"] == opt0

        client.close()

    # 2. Simulate server restart with expired deadline
    db_path = data_dir / "app.db"
    with sqlite3.connect(db_path) as conn:
        past_micro = int((time.time() - 3600) * 1000000)
        conn.execute("UPDATE session SET deadline_at = ? WHERE id = ?", (past_micro, exam_id))
        conn.execute("UPDATE exam_session SET deadline_at = ? WHERE session_id = ?", (past_micro, exam_id))
        conn.commit()

    # Restart server -> should settle expired exam on startup
    with managed_server(bin_path, data_dir, listen_port=0, log_dir=case_dir) as server2:
        artifacts.append(str(server2.log_path))
        client2 = APIClient(server2.base_url)
        client2.initialize_client_context()

        # Check exam is now submitted
        r_after = client2.get(f"/api/v1/exams/{exam_id}")
        assert r_after.status_code == 200, f"Expected 200, got {r_after.status_code}: {r_after.text}"
        assert r_after.json()["status"] == "submitted", f"Expected submitted, got: {r_after.text}"

        # Result report exists
        r_res = client2.get(f"/api/v1/exams/{exam_id}/result")
        assert r_res.status_code == 200
        assert r_res.json()["total"] == 3

        # Post-submit event rejected
        late_id = str(uuid.uuid4())
        r_late = client2.post(f"/api/v1/exams/{exam_id}/response-events", json={
            "instance_id": q0["instance_id"],
            "action": "select",
            "option_id": opt0,
            "client_event_id": late_id,
            "client_sequence": 2,
            "expected_revision": 1
        }, headers={"Idempotency-Key": late_id})
        assert r_late.status_code == 409
        assert r_late.json()["error"]["code"] in ("SESSION_CLOSED", "EXAM_SUBMITTED")

        # Now active session slot is free: can create new practice session!
        r_new_prac = client2.post("/api/v1/practice-sessions", json={
            "bank_id": BANK_ID
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert r_new_prac.status_code == 201, f"Expected 201 for practice session after exam settled, got {r_new_prac.status_code}"

        client2.close()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "session_slot_exclusivity": "ok",
            "draft_recovery": "ok",
            "startup_deadline_settle": "ok",
            "post_submit_rejection": "ok"
        }
    }
