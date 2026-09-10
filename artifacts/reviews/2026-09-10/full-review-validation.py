"""Isolated review probes. Assertions document observed defects, not desired behavior."""
import json
import sqlite3
import sys
import time
import uuid
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / 'e2e'))
from support.backend_case import BackendCase, BANK_ID, run_class


class ReviewProbes(BackendCase):
    def test_claim_limit_is_only_per_request(self):
        refs = [{'sense_id': sid, 'sense_version_id': vid} for sid, vid in self.rows('SELECT s.id, sv.id FROM bank_sense bs JOIN sense s ON s.id=bs.sense_id JOIN sense_version sv ON sv.sense_id=s.id WHERE bs.bank_id=? LIMIT 3', (BANK_ID,))]
        self.assertEqual(len(refs), 3)
        rev = self.rows('SELECT revision FROM bank WHERE id=?', (BANK_ID,))[0][0]
        self.post(f'/api/v1/banks/{BANK_ID}/generation-jobs', {'sense_refs': refs, 'generation_config_version': 'v1', 'quality_policy_version': 'v1', 'expected_revision': rev}, 202)
        token = (self.data_dir / 'worker.token').read_text().strip()
        counts = []
        for worker in ['review-worker-1', 'review-worker-2']:
            res = self.client.client.post('/internal/v1/generation/claim', json={'worker_id': worker, 'limit': 2}, headers={'Authorization': 'Bearer ' + token})
            self.assertEqual(res.status_code, 200, res.text)
            counts.append(len(res.json()['items']))
        self.assertEqual(counts, [2, 1])
        count = self.rows("SELECT count(*) FROM generation_item WHERE status='claimed'")[0][0]
        self.assertEqual(count, 3)
        (self.case_dir / 'claim-result.json').write_text(json.dumps({'configured_limit': 2, 'claim_counts': counts, 'claimed_items': count}, indent=2))

    def test_resolve_mutates_active_bank(self):
        bank = self.post('/api/v1/banks', {'name': 'review-bank'}, 201)
        bid = bank['bank_id']
        imp = self.post(f'/api/v1/banks/{bid}/imports', {'words': ['reviewunknown'], 'expected_revision': bank['revision']})
        item = self.get(f"/api/v1/imports/{imp['import_id']}/items")['items'][0]
        self.post('/api/v1/practice-sessions', {'bank_id': bid}, 201)
        sid = self.rows('SELECT id FROM sense LIMIT 1')[0][0]
        response = self.request('POST', f"/api/v1/import-items/{item['id']}/resolve", {'action': 'adopt', 'sense_refs': [sid], 'expected_revision': item['revision']})
        self.assertEqual(response.status_code, 200, response.text)
        self.assertEqual(self.rows('SELECT count(*) FROM bank_sense WHERE bank_id=?', (bid,))[0][0], 1)
        self.assertEqual(self.rows("SELECT count(*) FROM session WHERE status='open'")[0][0], 1)

    def test_idempotency_failure_still_commits_import(self):
        bank = self.post('/api/v1/banks', {'name': 'review-idempotency'}, 201)
        bid = bank['bank_id']
        self.fixture("CREATE TRIGGER review_idempotency_fault BEFORE INSERT ON idempotency_record BEGIN SELECT RAISE(ABORT, 'review injected statement failure'); END")
        body = {'words': ['reviewunknown'], 'expected_revision': bank['revision']}
        first = self.post(f'/api/v1/banks/{bid}/imports', body, key='review-same-key')
        second = self.post(f'/api/v1/banks/{bid}/imports', body, key='review-same-key')
        self.assertNotEqual(first['import_id'], second['import_id'])
        self.assertEqual(self.rows('SELECT count(*) FROM import_job WHERE target_id=?', (bid,))[0][0], 2)
        self.assertEqual(self.rows("SELECT count(*) FROM idempotency_record WHERE key='review-same-key'")[0][0], 0)

    def test_startup_ready_despite_expired_settlement_failure(self):
        self.create_exam(timed=True)
        exam_id = self.rows("SELECT id FROM session WHERE kind='exam'")[0][0]
        self.expire_exam(exam_id)
        self.fixture("CREATE TRIGGER review_assessment_fault BEFORE INSERT ON assessment_result BEGIN SELECT RAISE(ABORT, 'review injected settlement failure'); END")
        self.restart()
        self.assertEqual(self.rows('SELECT status FROM session WHERE id=?', (exam_id,))[0][0], 'open')
        self.get('/api/v1/health')

    def test_rebuild_ignores_selected_policy(self):
        custom = json.loads((ROOT / 'config/policy-v2.json').read_text())
        custom['fsrs']['learning_steps_minutes'] = [1, 60]
        custom_path = self.case_dir / 'custom-policy.json'
        custom_path.write_text(json.dumps(custom))
        self.context = dict(self.context, policy_path=custom_path)
        self.restart()
        session_id = self.create_practice()
        nxt = self.post(f'/api/v1/practice-sessions/{session_id}/next', {'expected_cursor': 0})
        iid = nxt['question']['instance_id']
        snap = json.loads(self.rows('SELECT snapshot_json FROM question_instance WHERE id=?', (iid,))[0][0])
        self.post(f'/api/v1/practice-sessions/{session_id}/attempts', {'instance_id': iid, 'option_id': snap['correct_option_id']})
        rev = self.rows('SELECT revision FROM session WHERE id=?', (session_id,))[0][0]
        self.post(f'/api/v1/practice-sessions/{session_id}/end', {'expected_revision': rev, 'reason': 'ended'})
        before = self.rows('SELECT sense_id, task_family, due_at-last_review_at FROM memory_projection')[0]
        used = self.rows('SELECT question_version_id FROM question_instance WHERE id=?', (iid,))[0][0]
        qv, qrev = self.rows("SELECT id,revision FROM question_version WHERE id != ? AND status='published' LIMIT 1", (used,))[0]
        self.post(f'/api/v1/question-versions/{qv}/evidence-dispositions', {'action': 'disputed', 'reason': 'review unrelated version', 'expected_revision': qrev})
        after = self.rows('SELECT due_at-last_review_at FROM memory_projection WHERE projection_version=(SELECT active_version FROM projection_control) AND sense_id=? AND task_family=?', before[:2])[0][0]
        (self.case_dir / 'policy-result.json').write_text(json.dumps({'before_due_offset_us': before[2], 'after_due_offset_us': after}, indent=2))
        self.assertNotEqual(before[2], after)


if __name__ == '__main__':
    case_dir = Path(__file__).parent / ('full-review-probes-' + uuid.uuid4().hex[:8])
    case_dir.mkdir()
    result = run_class(ReviewProbes, {'case_dir': case_dir, 'root_dir': ROOT, 'policy_path': ROOT / 'config/policy-v2.json'})
    (case_dir / 'result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2))
    print(json.dumps(result, ensure_ascii=False, indent=2))
    sys.exit(result['status'] != 'passed')
