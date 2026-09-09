"""PR-05: Simultaneous HTTP writes, exactly-once effects, and SIGKILL retries."""
from concurrent.futures import ThreadPoolExecutor
from threading import Barrier
import uuid
from support.backend_case import BackendCase, BANK_ID, run_class
from support.http_client import APIClient


class PracticeBoundaryTests(BackendCase):
    def test_end_replay_survives_sigkill_and_checks_digest(self):
        sid = self.create_practice()
        path, key = f"/api/v1/practice-sessions/{sid}/end", str(uuid.uuid4())
        body = {"expected_revision": 0, "reason": "ended"}
        accepted = self.post(path, body, key=key)
        self.restart(crash=True)
        replay = self.request("POST", path, body, key)
        self.assertEqual(replay.status_code, 200, replay.text)
        self.assertEqual(replay.json(), accepted)
        self.assertEqual(replay.headers.get("Idempotent-Replayed"), "true")
        self.error(self.request("POST", path, {**body, "reason": "abandoned"}, key), "IDEMPOTENCY_CONFLICT")
        self.assertEqual(self.rows("SELECT status, revision FROM session WHERE id=?", (sid,)), [("ended", 1)])

    def test_invalid_end_reason_does_not_change_session(self):
        sid = self.create_practice()
        response = self.request("POST", f"/api/v1/practice-sessions/{sid}/end",
                                {"expected_revision": 0, "reason": "submitted"})
        self.error(response, "INVALID_REQUEST", 400)
        self.assertEqual(self.rows("SELECT status, revision FROM session WHERE id=?", (sid,)), [("open", 0)])

    def test_simultaneous_duplicate_attempt_has_one_business_effect(self):
        sid = self.create_practice()
        q = self.post(f"/api/v1/practice-sessions/{sid}/next", {"expected_cursor": 0})["question"]
        body = {"instance_id": q["instance_id"], "option_id": q["options"][0]["id"]}
        path, key = f"/api/v1/practice-sessions/{sid}/attempts", str(uuid.uuid4())
        gate = Barrier(8)
        def send(_):
            gate.wait(timeout=10)
            return self.request("POST", path, body, key)
        with ThreadPoolExecutor(max_workers=8) as pool:
            replies = list(pool.map(send, range(8)))
        for response in replies:
            self.assertEqual(response.status_code, 200, response.text)
            self.assertEqual(response.json(), replies[0].json())
        self.assertEqual(sum(r.headers.get("Idempotent-Replayed") == "true" for r in replies), 7)
        aid = replies[0].json()["attempt_id"]
        self.assertEqual(self.rows("SELECT count(*) FROM attempt_event WHERE instance_id=?", (q["instance_id"],)), [(1,)])
        self.assertEqual(self.rows("SELECT count(*) FROM review_event WHERE attempt_id=?", (aid,)), [(1,)])
        self.assertEqual(self.rows("SELECT count(*) FROM exposure_event WHERE source_instance_id=? AND kind='feedback_available'", (q["instance_id"],)), [(1,)])
        self.restart(crash=True)
        self.assertEqual(self.post(path, body, key=key), replies[0].json())

    def test_cross_mode_creation_race_keeps_one_active_session(self):
        gate = Barrier(2)
        def create(kind):
            client = APIClient(self.server.base_url)
            try:
                client.initialize_client_context()
                gate.wait(timeout=10)
                body = {"bank_id": BANK_ID}
                if kind == "exams":
                    body["question_count"] = 3
                return client.post(f"/api/v1/{kind}", json=body,
                                   headers={"Idempotency-Key": str(uuid.uuid4())})
            finally:
                client.close()
        with ThreadPoolExecutor(max_workers=2) as pool:
            responses = list(pool.map(create, ["practice-sessions", "exams"]))
        self.assertEqual(sorted(r.status_code for r in responses), [201, 409], [r.text for r in responses])
        self.error(next(r for r in responses if r.status_code == 409), "SESSION_ACTIVE")
        self.assertEqual(self.rows("SELECT count(*) FROM session WHERE status='open'"), [(1,)])


def run(context):
    return run_class(PracticeBoundaryTests, context)
