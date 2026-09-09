"""EX-02: Exam response events, convergence and rebase protocol acceptance test.
Conforms strictly to 10 R01:
- Dual-tab same-watermark concurrency & explicit rejection with acceptance="not_accepted";
- Automatic rebase retaining client_event_id and action;
- Dropped response idempotency replay;
- Gap rejection;
- Telemetry-gaps CAS barrier blocking late old-revision requests.
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
    data_dir = case_dir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)
    artifacts = []

    with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))
        client = APIClient(server.base_url)
        client.initialize_client_context()

        # 1. Create exam
        create_key = str(uuid.uuid4())
        resp = client.post("/api/v1/exams", json={
            "bank_id": BANK_ID,
            "question_count": 3
        }, headers={"Idempotency-Key": create_key})
        assert resp.status_code == 201
        exam_data = resp.json()
        exam_id = exam_data["exam_id"]
        q0 = exam_data["public_questions"][0]
        q1 = exam_data["public_questions"][1]

        opt0 = q0["options"][0]["id"]
        opt1 = q1["options"][0]["id"]

        # 2. Dual-tab race on revision=0, sequence=0
        # Tab A sends event A (select opt on q0)
        client_event_id_A = str(uuid.uuid4())
        payload_A = {
            "instance_id": q0["instance_id"],
            "action": "select",
            "option_id": opt0,
            "client_event_id": client_event_id_A,
            "client_sequence": 1,
            "expected_revision": 0
        }
        resp_A = client.post(f"/api/v1/exams/{exam_id}/response-events", json=payload_A, headers={"Idempotency-Key": client_event_id_A})
        assert resp_A.status_code == 200, f"Event A failed: {resp_A.text}"
        data_A = resp_A.json()
        assert data_A["accepted_sequence"] == 1
        assert data_A["current_revision"] == 1

        # Tab B sends event B with stale expected_revision=0, sequence=1
        client_event_id_B = str(uuid.uuid4())
        payload_B = {
            "instance_id": q1["instance_id"],
            "action": "select",
            "option_id": opt1,
            "client_event_id": client_event_id_B,
            "client_sequence": 1,
            "expected_revision": 0
        }
        resp_B = client.post(f"/api/v1/exams/{exam_id}/response-events", json=payload_B, headers={"Idempotency-Key": client_event_id_B})
        assert resp_B.status_code == 409, f"Expected 409 conflict, got {resp_B.status_code}: {resp_B.text}"
        err_B = resp_B.json()["error"]
        assert err_B["code"] == "REVISION_CONFLICT"
        assert err_B["details"]["acceptance"] == "not_accepted"
        assert err_B["details"]["current_revision"] == 1

        # Verify receipt for event B is 404
        receipt_B_404 = client.get(f"/api/v1/exams/{exam_id}/response-events/{client_event_id_B}")
        assert receipt_B_404.status_code == 404

        # 3. Tab B rebases event B: keep client_event_id_B, update sequence=2, revision=1, add rebased_from
        payload_B_rebased = {
            "instance_id": q1["instance_id"],
            "action": "select",
            "option_id": opt1,
            "client_event_id": client_event_id_B,
            "client_sequence": 2,
            "expected_revision": 1,
            "rebased_from": {
                "client_sequence": 1,
                "expected_revision": 0
            }
        }
        resp_B_rebased = client.post(f"/api/v1/exams/{exam_id}/response-events", json=payload_B_rebased, headers={"Idempotency-Key": client_event_id_B})
        assert resp_B_rebased.status_code == 200, f"Rebased event B failed: {resp_B_rebased.text}"
        data_B_rebased = resp_B_rebased.json()
        assert data_B_rebased["accepted_sequence"] == 2
        assert data_B_rebased["current_revision"] == 2

        # Verify receipt for event B now exists
        receipt_B = client.get(f"/api/v1/exams/{exam_id}/response-events/{client_event_id_B}")
        assert receipt_B.status_code == 200
        assert receipt_B.json()["client_sequence"] == 2
        assert receipt_B.json()["accepted_revision"] == 2

        # 4. Dropped response idempotency test: replay exact event B payload
        resp_B_replay = client.post(f"/api/v1/exams/{exam_id}/response-events", json=payload_B_rebased, headers={"Idempotency-Key": client_event_id_B})
        assert resp_B_replay.status_code == 200
        assert resp_B_replay.headers.get("Idempotent-Replayed") == "true"
        assert resp_B_replay.json()["current_revision"] == 2

        # 5. Gap rejection test: client skips sequence to 5
        client_event_id_gap = str(uuid.uuid4())
        payload_gap = {
            "instance_id": q0["instance_id"],
            "action": "clear",
            "client_event_id": client_event_id_gap,
            "client_sequence": 5,
            "expected_revision": 2
        }
        resp_gap = client.post(f"/api/v1/exams/{exam_id}/response-events", json=payload_gap, headers={"Idempotency-Key": client_event_id_gap})
        assert resp_gap.status_code == 409
        err_gap = resp_gap.json()["error"]
        assert err_gap["code"] == "EVENT_SEQUENCE_GAP"
        assert err_gap["details"]["acceptance"] == "not_accepted"
        assert err_gap["details"]["expected_next_sequence"] == 3

        # 6. Telemetry-Gaps CAS barrier
        key_gap_barrier = str(uuid.uuid4())
        resp_barrier = client.post(f"/api/v1/exams/{exam_id}/telemetry-gaps", json={
            "expected_revision": 2,
            "reason": "lost_queue"
        }, headers={"Idempotency-Key": key_gap_barrier})
        assert resp_barrier.status_code == 200, f"Telemetry gap barrier failed: {resp_barrier.text}"
        data_barrier = resp_barrier.json()
        assert data_barrier["telemetry_incomplete"] is True
        assert data_barrier["current_revision"] == 3

        # After barrier, late request with expected_revision=2 is rejected
        client_event_id_late = str(uuid.uuid4())
        payload_late = {
            "instance_id": q0["instance_id"],
            "action": "clear",
            "client_event_id": client_event_id_late,
            "client_sequence": 3,
            "expected_revision": 2
        }
        resp_late = client.post(f"/api/v1/exams/{exam_id}/response-events", json=payload_late, headers={"Idempotency-Key": client_event_id_late})
        assert resp_late.status_code == 409
        assert resp_late.json()["error"]["code"] == "REVISION_CONFLICT"
        assert resp_late.json()["error"]["details"]["acceptance"] == "not_accepted"

        # Verify DB state: 2 response events, telemetry_incomplete=1
        db_path = data_dir / "app.db"
        with sqlite3.connect(db_path) as conn:
            cursor = conn.cursor()
            cursor.execute("SELECT count(*) FROM response_event WHERE session_id = ?", (exam_id,))
            ev_count = cursor.fetchone()[0]
            assert ev_count == 2, f"Expected exactly 2 response events in DB, got {ev_count}"

            cursor.execute("SELECT telemetry_incomplete FROM exam_session WHERE session_id = ?", (exam_id,))
            ti = cursor.fetchone()[0]
            assert ti == 1, "Expected telemetry_incomplete=1 in exam_session"

        client.close()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "dual_tab_rebase": "ok",
            "dropped_response_replay": "ok",
            "gap_rejection": "ok",
            "telemetry_gap_barrier": "ok"
        }
    }
