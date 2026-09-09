"""EX-05: Live deadline settlement, revision CAS and durable receipt regressions."""
import uuid
from support.backend_case import BackendCase, run_class


class ExamBoundaryTests(BackendCase):
    def event(self, exam, revision=0, sequence=1):
        q = exam["public_questions"][0]
        return {"instance_id": q["instance_id"], "action": "select",
                "option_id": q["options"][0]["id"], "client_event_id": str(uuid.uuid4()),
                "client_sequence": sequence, "expected_revision": revision}

    def test_expired_event_commits_settlement_and_survives_crash(self):
        exam = self.create_exam(timed=True)
        eid = exam["exam_id"]
        self.expire_exam(eid)
        event = self.event(exam)
        response = self.request("POST", f"/api/v1/exams/{eid}/response-events", event, event["client_event_id"])
        self.error(response, "EXAM_SUBMITTED")
        self.assertEqual(self.get(f"/api/v1/exams/{eid}")["status"], "submitted")
        report = self.get(f"/api/v1/exams/{eid}/result")
        self.assertEqual(report["total"], 3)
        self.assertEqual(self.rows("SELECT count(*) FROM response_event WHERE session_id=?", (eid,)), [(0,)])
        self.assertEqual(self.rows("SELECT count(*) FROM assessment_result WHERE session_id=?", (eid,)), [(1,)])
        self.restart(crash=True)
        self.assertEqual(self.get(f"/api/v1/exams/{eid}/result"), report)
        self.create_practice()  # Automatic settlement must free the global active slot.

    def test_expired_submit_uses_incomplete_telemetry(self):
        exam = self.create_exam(timed=True)
        eid = exam["exam_id"]
        event = self.event(exam)
        self.post(f"/api/v1/exams/{eid}/response-events", event, key=event["client_event_id"])
        self.expire_exam(eid)
        key = str(uuid.uuid4())
        self.post(f"/api/v1/exams/{eid}/submit", {"expected_revision": 1,
                  "last_ack_sequence": 1, "submission_id": key}, key=key)
        self.assertEqual(self.rows("SELECT telemetry_incomplete FROM exam_session WHERE session_id=?", (eid,)), [(1,)])
        histories = self.rows("SELECT DISTINCT response_history FROM assessment_item_result WHERE assessment_id IN (SELECT id FROM assessment_result WHERE session_id=?)", (eid,))
        self.assertEqual(histories, [("telemetry_incomplete",)])
        self.assertEqual(self.rows("SELECT count(*) FROM review_event WHERE attempt_id IN (SELECT id FROM attempt_event WHERE session_id=?)", (eid,)), [(0,)])

    def test_stale_abandon_cannot_close_newer_draft(self):
        exam = self.create_exam()
        eid = exam["exam_id"]
        event = self.event(exam)
        self.post(f"/api/v1/exams/{eid}/response-events", event, key=event["client_event_id"])
        response = self.request("POST", f"/api/v1/exams/{eid}/abandon", {"expected_revision": 0})
        self.error(response, "REVISION_CONFLICT")
        self.assertEqual(response.json()["error"]["details"]["current_revision"], 1)
        self.assertEqual(self.get(f"/api/v1/exams/{eid}")["status"], "open")
        self.assertEqual(self.rows("SELECT count(*) FROM assessment_result WHERE session_id=?", (eid,)), [(0,)])
        self.post(f"/api/v1/exams/{eid}/abandon", {"expected_revision": 1})

    def test_accepted_event_replays_after_submit_and_restart(self):
        exam = self.create_exam()
        eid = exam["exam_id"]
        event = self.event(exam)
        path = f"/api/v1/exams/{eid}/response-events"
        accepted = self.post(path, event, key=event["client_event_id"])
        key = str(uuid.uuid4())
        self.post(f"/api/v1/exams/{eid}/submit", {"expected_revision": 1,
                  "last_ack_sequence": 1, "submission_id": key}, key=key)
        self.restart(crash=True)
        retry = self.request("POST", path, event, event["client_event_id"])
        self.assertEqual(retry.status_code, 200, retry.text)
        self.assertEqual(retry.json(), accepted)
        self.assertEqual(retry.headers.get("Idempotent-Replayed"), "true")
        self.assertEqual(self.rows("SELECT count(*) FROM response_event WHERE session_id=?", (eid,)), [(1,)])

    def test_submit_key_rejects_changed_body_after_completion(self):
        exam = self.create_exam()
        eid, key = exam["exam_id"], str(uuid.uuid4())
        path = f"/api/v1/exams/{eid}/submit"
        body = {"expected_revision": 0, "last_ack_sequence": 0, "submission_id": key}
        accepted = self.post(path, body, key=key)
        self.assertEqual(self.post(path, body, key=key), accepted)
        response = self.request("POST", path, {**body, "expected_revision": 100}, key)
        self.error(response, "IDEMPOTENCY_CONFLICT")
        self.assertEqual(self.rows("SELECT count(*) FROM assessment_result WHERE session_id=?", (eid,)), [(1,)])


def run(context):
    return run_class(ExamBoundaryTests, context)
