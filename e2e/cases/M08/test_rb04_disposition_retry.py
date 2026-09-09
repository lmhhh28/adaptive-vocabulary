"""RB-04: Evidence disposition is durable and only rebuilds once on HTTP retry."""
import uuid
from support.backend_case import BackendCase, run_class


class EvidenceRetryTests(BackendCase):
    def test_retry_after_restart_preserves_disposition_and_projection(self):
        qid, revision = self.rows("SELECT id, revision FROM question_version WHERE status='published' LIMIT 1")[0]
        key = str(uuid.uuid4())
        path = f"/api/v1/question-versions/{qid}/evidence-dispositions"
        body = {"action": "disputed", "reason": "HTTP response lost", "expected_revision": revision}
        first = self.post(path, body, key=key)
        self.assertEqual(first["status"], "succeeded")
        version = self.rows("SELECT active_version FROM projection_control")
        self.restart(crash=True)
        retry = self.request("POST", path, body, key)
        self.assertEqual(retry.status_code, 200, retry.text)
        self.assertEqual(retry.json(), first)
        self.assertEqual(retry.headers.get("Idempotent-Replayed"), "true")
        self.error(self.request("POST", path, {**body, "reason": "different request"}, key), "IDEMPOTENCY_CONFLICT")
        self.assertEqual(self.rows("SELECT count(*) FROM evidence_disposition WHERE question_version_id=?", (qid,)), [(1,)])
        self.assertEqual(self.rows("SELECT count(*) FROM model_rebuild_run WHERE kind='evidence_revision'"), [(1,)])
        self.assertEqual(self.rows("SELECT revision FROM question_version WHERE id=?", (qid,)), [(revision + 1,)])
        self.assertEqual(self.rows("SELECT active_version FROM projection_control"), version)
        self.create_practice()
        # Already accepted receipt stays readable even when a new session is active.
        self.assertEqual(self.post(path, body, key=key), first)


def run(context):
    return run_class(EvidenceRetryTests, context)
