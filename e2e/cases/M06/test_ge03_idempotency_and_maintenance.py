"""GE-03: Idempotency, maintenance gate and question instance publishing acceptance test.
Verifies candidate/settle idempotency, differing settlement digest conflict,
maintenance mode pauses publishing while saving candidate, and instances only reference published versions.
"""
from pathlib import Path
import sqlite3
import uuid
from support.lifecycle import managed_server
from support.http_client import APIClient
from support.scripted_worker import ScriptedWorker


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    data_dir = case_dir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)
    artifacts = []

    with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        base_url = server.base_url
        artifacts.append(str(server.log_path))

        token_path = data_dir / "worker.token"
        worker_token = token_path.read_text().strip()

        db_path = data_dir / "app.db"
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("SELECT id, revision FROM bank WHERE name = '开发样例'")
        bank_id, bank_rev = cur.fetchone()

        cur.execute("""
            SELECT s.id, sv.id
            FROM sense s
            JOIN sense_version sv ON sv.sense_id = s.id
            JOIN bank_sense bs ON bs.sense_id = s.id
            WHERE bs.bank_id = ?
            LIMIT 1
        """, (bank_id,))
        sense_id, sense_version_id = cur.fetchone()
        conn.close()

        # 1. Create generation job
        client = APIClient(base_url)
        client.initialize_client_context()
        create_resp = client.post(f"/api/v1/banks/{bank_id}/generation-jobs", json={
            "sense_refs": [{"sense_id": sense_id, "sense_version_id": sense_version_id}],
            "generation_config_version": "v1",
            "quality_policy_version": "v1",
            "expected_revision": bank_rev,
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert create_resp.status_code == 202
        job_id = create_resp.json()["job_id"]

        # 2. Worker claims item
        worker = ScriptedWorker(base_url, worker_token, worker_id="worker-idemp")
        claim_resp = worker.claim(limit=1)
        assert claim_resp.status_code == 200
        item = claim_resp.json()["items"][0]

        # 3. Reserve and test settlement idempotency & conflict
        key_gen = {"attempt_number": 1, "stage": "generator", "candidate_index": 0}
        r_gen = worker.reserve(item["item_id"], item["fencing_token"], item["input_digest"], key_gen, str(uuid.uuid4()))
        assert r_gen.status_code == 200
        gen_res_id = r_gen.json()["reservation_id"]

        # Settle once
        s1 = worker.settle(gen_res_id, item["item_id"], item["fencing_token"], item["input_digest"], outcome="response", usage={"input_tokens": 100, "output_tokens": 100})
        assert s1.status_code == 200
        data1 = s1.json()

        # Settle again with exact same payload -> idempotent replay
        s1_replay = worker.settle(gen_res_id, item["item_id"], item["fencing_token"], item["input_digest"], outcome="response", usage={"input_tokens": 100, "output_tokens": 100})
        assert s1_replay.status_code == 200
        assert s1_replay.json()["accounted_tokens"] == data1["accounted_tokens"]

        # Settle with different outcome -> 409 CALL_SETTLEMENT_CONFLICT
        s1_conflict = worker.settle(gen_res_id, item["item_id"], item["fencing_token"], item["input_digest"], outcome="network_error")
        assert s1_conflict.status_code == 409, f"Expected 409, got {s1_conflict.status_code}"
        assert s1_conflict.json()["error"]["code"] == "CALL_SETTLEMENT_CONFLICT"

        # Reserve 2 evaluator calls
        k_eval1 = {"attempt_number": 1, "stage": "evaluator", "candidate_index": 0, "evaluation_index": 1}
        r_eval1 = worker.reserve(item["item_id"], item["fencing_token"], item["input_digest"], k_eval1, str(uuid.uuid4()))
        assert r_eval1.status_code == 200
        eval1_res_id = r_eval1.json()["reservation_id"]
        worker.settle(eval1_res_id, item["item_id"], item["fencing_token"], item["input_digest"], outcome="response", usage={"input_tokens": 50, "output_tokens": 50})

        k_eval2 = {"attempt_number": 1, "stage": "evaluator", "candidate_index": 0, "evaluation_index": 2}
        r_eval2 = worker.reserve(item["item_id"], item["fencing_token"], item["input_digest"], k_eval2, str(uuid.uuid4()))
        assert r_eval2.status_code == 200
        eval2_res_id = r_eval2.json()["reservation_id"]
        worker.settle(eval2_res_id, item["item_id"], item["fencing_token"], item["input_digest"], outcome="response", usage={"input_tokens": 50, "output_tokens": 50})

        # 4. Candidate submission with Idempotency-Key
        cand_content = {
            "task_family": "meaning_recognition",
            "stem": "Which option translates the word accurately?",
            "options": [
                {"text": "正确答案释义", "is_correct": True, "language": "zh", "sense_id": sense_id},
                {"text": "干扰项X", "is_correct": False, "language": "zh"},
                {"text": "干扰项Y", "is_correct": False, "language": "zh"},
                {"text": "干扰项Z", "is_correct": False, "language": "zh"},
            ],
            "explanation": "解析内容"
        }
        cand_checks = {
            "rule_checks": {"options_count": 4},
            "semantic_evaluations": [
                {"profile_version": "v1", "model": "e1", "ratings": {"target_meaning": "pass", "unique_answer": "pass", "grammar": "pass", "naturalness": "pass", "explanation_consistency": "pass", "distractor_validity": "pass"}, "rationale": "ok"},
                {"profile_version": "v1", "model": "e2", "ratings": {"target_meaning": "pass", "unique_answer": "pass", "grammar": "pass", "naturalness": "pass", "explanation_consistency": "pass", "distractor_validity": "pass"}, "rationale": "ok"}
            ],
            "result": "pass"
        }

        idemp_key = str(uuid.uuid4())
        sub1 = worker.submit_candidate(
            item["item_id"], item["fencing_token"], item["input_digest"],
            attempt_number=1, candidate_index=0,
            content=cand_content,
            generator_reservation_id=gen_res_id,
            evaluator_reservation_ids=[eval1_res_id, eval2_res_id],
            checks=cand_checks,
            idempotency_key=idemp_key
        )
        assert sub1.status_code == 200, f"Submit failed: {sub1.text}"
        sub1_data = sub1.json()
        assert sub1_data["disposition"] == "published"
        pub_ver_id = sub1_data["published_version_id"]

        # Resend exact same submission with same Idempotency-Key -> returns same response
        sub1_replay = worker.submit_candidate(
            item["item_id"], item["fencing_token"], item["input_digest"],
            attempt_number=1, candidate_index=0,
            content=cand_content,
            generator_reservation_id=gen_res_id,
            evaluator_reservation_ids=[eval1_res_id, eval2_res_id],
            checks=cand_checks,
            idempotency_key=idemp_key
        )
        assert sub1_replay.status_code == 200
        assert sub1_replay.json()["disposition"] == "published"
        assert sub1_replay.json()["published_version_id"] == pub_ver_id

        # Resend with same Idempotency-Key but different payload -> 409 IDEMPOTENCY_CONFLICT
        sub1_differ = worker.submit_candidate(
            item["item_id"], item["fencing_token"], item["input_digest"],
            attempt_number=1, candidate_index=1,
            content=cand_content,
            generator_reservation_id=gen_res_id,
            evaluator_reservation_ids=[eval1_res_id, eval2_res_id],
            checks=cand_checks,
            idempotency_key=idemp_key
        )
        assert sub1_differ.status_code == 409, f"Expected 409 IDEMPOTENCY_CONFLICT, got {sub1_differ.status_code}"
        assert sub1_differ.json()["error"]["code"] == "IDEMPOTENCY_CONFLICT"

        # 5. Maintenance Mode: enter maintenance mode in DB
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("UPDATE projection_control SET mode = 'rebuilding' WHERE id = 1")
        conn.commit()
        conn.close()

        # Submit candidate while in maintenance mode
        cand_maint_content = dict(cand_content)
        cand_maint_content["stem"] = "Maintenance candidate stem?"
        sub_maint = worker.submit_candidate(
            item["item_id"], item["fencing_token"], item["input_digest"],
            attempt_number=1, candidate_index=2,
            content=cand_maint_content,
            generator_reservation_id=gen_res_id,
            evaluator_reservation_ids=[eval1_res_id, eval2_res_id],
            checks=cand_checks
        )
        assert sub_maint.status_code == 200, f"Submit during maintenance failed: {sub_maint.text}"
        maint_data = sub_maint.json()
        assert maint_data["disposition"] == "pending_maintenance", f"Expected pending_maintenance, got {maint_data['disposition']}"
        assert maint_data.get("published_version_id") is None, "Must not publish question_version during maintenance"

        # Verify in DB: candidate row exists with 'pending_maintenance', but question_version does not have it
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("SELECT disposition, published_version_id FROM question_candidate WHERE id = ?", (maint_data["candidate_id"],))
        row = cur.fetchone()
        assert row is not None
        assert row[0] == "pending_maintenance"
        assert row[1] is None

        # 6. Verify practice sessions only reference 'published' question versions
        cur.execute("UPDATE projection_control SET mode = 'active' WHERE id = 1")
        conn.commit()
        conn.close()

        practice_resp = client.post("/api/v1/practice-sessions", json={"bank_id": bank_id}, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert practice_resp.status_code in {200, 201}
        practice_id = practice_resp.json()["session"]["id"]

        next_resp = client.post(f"/api/v1/practice-sessions/{practice_id}/next", json={"expected_cursor": 0})
        assert next_resp.status_code == 200
        if next_resp.json().get("status") == "ready":
            inst_id = next_resp.json()["question"]["instance_id"]
            conn = sqlite3.connect(db_path)
            cur = conn.cursor()
            cur.execute("""
                SELECT qv.status
                FROM question_instance qi
                JOIN question_version qv ON qv.id = qi.question_version_id
                WHERE qi.id = ?
            """, (inst_id,))
            inst_status = cur.fetchone()[0]
            conn.close()
            assert inst_status == "published", f"Question instance referenced non-published version: {inst_status}"

        worker.close()
        client.close()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "checks_verified": [
                "settlement_idempotency_and_conflict",
                "candidate_submission_idempotency_key",
                "idempotency_key_payload_conflict",
                "maintenance_pauses_publishing_pending_maintenance",
                "practice_only_references_published_versions"
            ]
        }
    }
