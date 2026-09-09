"""GE-04: Lease/counter boundaries and publication proof cannot be bypassed."""
import uuid
from support.backend_case import BackendCase, BANK_ID, run_class
from support.scripted_worker import ScriptedWorker


class GenerationLeaseTests(BackendCase):
    def job(self):
        revision = self.rows("SELECT revision FROM bank WHERE id=?", (BANK_ID,))[0][0]
        senses = self.rows("SELECT s.id, sv.id FROM sense s JOIN sense_version sv ON sv.sense_id=s.id JOIN bank_sense bs ON bs.sense_id=s.id WHERE bs.bank_id=? LIMIT 2", (BANK_ID,))
        self.assertEqual(len(senses), 2)
        job = self.post(f"/api/v1/banks/{BANK_ID}/generation-jobs", {
            "expected_revision": revision, "generation_config_version": "v1",
            "quality_policy_version": "v1", "sense_refs": [
                {"sense_id": sid, "sense_version_id": vid} for sid, vid in senses]}, 202)
        token = (self.data_dir / "worker.token").read_text().strip()
        worker = ScriptedWorker(self.server.base_url, token, "lease-boundary-worker")
        self.addCleanup(worker.close)
        claim = worker.claim(1)
        self.assertEqual(claim.status_code, 200, claim.text)
        return job["job_id"], worker, claim.json()["items"][0]

    def test_expired_lease_cannot_complete_and_remains_claimable(self):
        jid, worker, item = self.job()
        self.fixture("UPDATE generation_item SET lease_until=1 WHERE id=?", (item["item_id"],))
        response = worker.complete(item["item_id"], item["fencing_token"], item["input_digest"])
        self.error(response, "LEASE_LOST")
        self.assertEqual(self.rows("SELECT pending_count FROM generation_job WHERE id=?", (jid,)), [(2,)])
        self.assertEqual(self.rows("SELECT status FROM generation_item WHERE id=?", (item["item_id"],)), [("claimed",)])
        new_items = worker.claim(10).json()["items"]
        replacement = next(i for i in new_items if i["item_id"] == item["item_id"])
        self.assertGreater(replacement["fencing_token"], item["fencing_token"])

    def test_duplicate_completion_does_not_decrement_pending_twice(self):
        jid, worker, item = self.job()
        args = (item["item_id"], item["fencing_token"], item["input_digest"])
        first = worker.complete(*args)
        self.assertEqual(first.status_code, 200, first.text)
        second = worker.complete(*args)
        self.assertEqual(second.status_code, 200, second.text)
        self.assertEqual(second.json(), first.json())
        self.assertEqual(self.rows("SELECT pending_count FROM generation_job WHERE id=?", (jid,)), [(1,)])
        self.assertEqual(self.rows("SELECT revision FROM generation_item WHERE id=?", (item["item_id"],)), [(2,)])
        conflict = worker.complete(*args, outcome="failed")
        self.assertEqual(conflict.status_code, 409, conflict.text)
        self.assertEqual(self.rows("SELECT status FROM generation_item WHERE id=?", (item["item_id"],)), [("completed",)])

    def candidate(self, worker, item, generator, evaluators, *, index=0, ratings=None):
        if ratings is None:
            ratings = {key: "pass" for key in ("target_meaning", "unique_answer", "grammar",
                       "naturalness", "explanation_consistency", "distractor_validity")}
        sense_id = self.rows("SELECT sense_id FROM generation_item WHERE id=?", (item["item_id"],))[0][0]
        return worker.submit_candidate(item["item_id"], item["fencing_token"], item["input_digest"],
            attempt_number=item["attempt_number"], candidate_index=index,
            content={"task_family": "meaning_recognition", "stem": "Select the meaning.",
                     "options": [{"text": "释义", "is_correct": True, "language": "zh", "sense_id": sense_id}] +
                                [{"text": f"干扰项{i}", "is_correct": False, "language": "zh"} for i in range(3)],
                     "explanation": "测试解析"},
            generator_reservation_id=generator, evaluator_reservation_ids=evaluators,
            checks={"result": "pass", "semantic_evaluations": [
                {"profile_version": "v1", "model": f"evaluator-{i}", "ratings": ratings, "rationale": "ok"}
                for i in range(2)]})

    def test_one_unsettled_call_cannot_impersonate_three_quality_checks(self):
        _, worker, item = self.job()
        response = worker.reserve(item["item_id"], item["fencing_token"], item["input_digest"],
            {"attempt_number": item["attempt_number"], "stage": "generator", "candidate_index": 0}, str(uuid.uuid4()))
        self.assertEqual(response.status_code, 200, response.text)
        rid = response.json()["reservation_id"]
        before = self.rows("SELECT count(*) FROM question_version")
        self.error(self.candidate(worker, item, rid, [rid, rid]), "INVALID_REQUEST", 400)
        self.assertEqual(self.rows("SELECT count(*) FROM question_candidate"), [(0,)])
        self.assertEqual(self.rows("SELECT count(*) FROM question_version"), before)

    def test_reservation_stage_candidate_and_evaluation_identity_are_checked(self):
        _, worker, item = self.job()
        generator, evaluators = worker.evaluated_reservations(item, 0)
        before = self.rows("SELECT count(*) FROM question_version")
        for gen, evals, index in [(evaluators[0], [generator, evaluators[1]], 0),
                                  (generator, [evaluators[0], evaluators[0]], 0),
                                  (generator, evaluators, 1)]:
            with self.subTest(generator=gen, evaluators=evals, candidate=index):
                self.error(self.candidate(worker, item, gen, evals, index=index), "INVALID_REQUEST", 400)
        self.assertEqual(self.rows("SELECT count(*) FROM question_candidate"), [(0,)])
        self.assertEqual(self.rows("SELECT count(*) FROM question_version"), before)
        valid = self.candidate(worker, item, generator, evaluators)
        self.assertEqual(valid.status_code, 200, valid.text)
        self.assertEqual(valid.json()["disposition"], "published")

    def test_empty_semantic_ratings_cannot_publish(self):
        _, worker, item = self.job()
        generator, evaluators = worker.evaluated_reservations(item, 0)
        before = self.rows("SELECT count(*) FROM question_version")
        response = self.candidate(worker, item, generator, evaluators, ratings={})
        self.assertEqual(response.status_code, 200, response.text)
        self.assertEqual(response.json()["disposition"], "rejected")
        self.assertEqual(self.rows("SELECT count(*) FROM question_version"), before)


def run(context):
    return run_class(GenerationLeaseTests, context)
