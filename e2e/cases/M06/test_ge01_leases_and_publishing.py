"""GE-01: Generation leases and publishing acceptance test.
Verifies claim/renew/expiration takeover, fencing token rejection for superseded workers,
and publishing requires both semantic evaluations to pass.
"""
from pathlib import Path
import sqlite3
import uuid
from playwright.sync_api import sync_playwright
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

        # 1. Read worker token
        token_path = data_dir / "worker.token"
        assert token_path.is_file(), "worker.token file not found"
        worker_token = token_path.read_text().strip()

        # 2. Get bank ID and sense from DB
        db_path = data_dir / "app.db"
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("SELECT id, revision FROM bank WHERE name = '开发样例'")
        bank_row = cur.fetchone()
        assert bank_row is not None, "Bank '开发样例' not found"
        bank_id, bank_rev = bank_row[0], bank_row[1]

        cur.execute("""
            SELECT s.id, sv.id
            FROM sense s
            JOIN sense_version sv ON sv.sense_id = s.id
            JOIN bank_sense bs ON bs.sense_id = s.id
            WHERE bs.bank_id = ?
            LIMIT 1
        """, (bank_id,))
        sense_row = cur.fetchone()
        assert sense_row is not None, "Sense not found"
        sense_id, sense_version_id = sense_row[0], sense_row[1]
        conn.close()

        # 3. Create generation job via public API
        client = APIClient(base_url)
        client.initialize_client_context()
        create_resp = client.post(f"/api/v1/banks/{bank_id}/generation-jobs", json={
            "sense_refs": [{"sense_id": sense_id, "sense_version_id": sense_version_id}],
            "generation_config_version": "v1",
            "quality_policy_version": "v1",
            "expected_revision": bank_rev,
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert create_resp.status_code == 202, f"Expected 202, got {create_resp.status_code}: {create_resp.text}"
        job_id = create_resp.json()["job_id"]

        # 4. Worker 1 claims item
        worker1 = ScriptedWorker(base_url, worker_token, worker_id="worker-1")
        claim_resp = worker1.claim(limit=1)
        assert claim_resp.status_code == 200, f"Claim failed: {claim_resp.text}"
        items = claim_resp.json()["items"]
        assert len(items) == 1, f"Expected 1 claimed item, got {len(items)}"
        item = items[0]
        assert item["fencing_token"] == 1, f"Expected fencing_token 1, got {item['fencing_token']}"

        # 5. Worker 1 renews lease
        renew_resp = worker1.renew(item["item_id"], item["fencing_token"], item["input_digest"])
        assert renew_resp.status_code == 200, f"Renew failed: {renew_resp.text}"
        new_lease = renew_resp.json()["lease_until"]
        assert new_lease > item["lease_until"]

        # 6. Simulate lease expiration in DB
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("UPDATE generation_item SET lease_until = lease_until - 300000000 WHERE id = ?", (item["item_id"],))
        conn.commit()
        conn.close()

        # 7. Worker 2 claims the expired item -> gets fencing token 2
        worker2 = ScriptedWorker(base_url, worker_token, worker_id="worker-2")
        claim2_resp = worker2.claim(limit=1)
        assert claim2_resp.status_code == 200
        items2 = claim2_resp.json()["items"]
        assert len(items2) == 1
        item2 = items2[0]
        assert item2["item_id"] == item["item_id"]
        assert item2["fencing_token"] == 2, f"Expected fencing token 2, got {item2['fencing_token']}"

        # 8. Worker 1 attempts action with old fence (fencing_token = 1) -> 409 LEASE_LOST
        fail_renew = worker1.renew(item["item_id"], item["fencing_token"], item["input_digest"])
        assert fail_renew.status_code == 409, f"Expected 409, got {fail_renew.status_code}"
        assert fail_renew.json()["error"]["code"] == "LEASE_LOST"

        # 9. Worker 2 reserves calls (1 generator + 2 evaluators)
        gen_call_key = {"attempt_number": item2["attempt_number"], "stage": "generator", "candidate_index": 0}
        r_gen = worker2.reserve(item2["item_id"], item2["fencing_token"], item2["input_digest"], gen_call_key, str(uuid.uuid4()))
        assert r_gen.status_code == 200, f"Reserve generator failed: {r_gen.text}"
        gen_res_id = r_gen.json()["reservation_id"]
        s_gen = worker2.settle(gen_res_id, item2["item_id"], item2["fencing_token"], item2["input_digest"], outcome="response", usage={"input_tokens": 100, "output_tokens": 100})
        assert s_gen.status_code == 200

        eval1_key = {"attempt_number": item2["attempt_number"], "stage": "evaluator", "candidate_index": 0, "evaluation_index": 1}
        r_eval1 = worker2.reserve(item2["item_id"], item2["fencing_token"], item2["input_digest"], eval1_key, str(uuid.uuid4()))
        assert r_eval1.status_code == 200
        eval1_res_id = r_eval1.json()["reservation_id"]
        s_eval1 = worker2.settle(eval1_res_id, item2["item_id"], item2["fencing_token"], item2["input_digest"], outcome="response", usage={"input_tokens": 50, "output_tokens": 50})
        assert s_eval1.status_code == 200

        eval2_key = {"attempt_number": item2["attempt_number"], "stage": "evaluator", "candidate_index": 0, "evaluation_index": 2}
        r_eval2 = worker2.reserve(item2["item_id"], item2["fencing_token"], item2["input_digest"], eval2_key, str(uuid.uuid4()))
        assert r_eval2.status_code == 200
        eval2_res_id = r_eval2.json()["reservation_id"]
        s_eval2 = worker2.settle(eval2_res_id, item2["item_id"], item2["fencing_token"], item2["input_digest"], outcome="response", usage={"input_tokens": 50, "output_tokens": 50})
        assert s_eval2.status_code == 200

        # 10. Worker 2 submits candidate with both evaluators passing all 6 dimensions
        cand_content = {
            "task_family": "meaning_recognition",
            "stem": "Select the correct meaning of the word.",
            "options": [
                {"text": "正确中文词义", "is_correct": True, "language": "zh", "sense_id": sense_id},
                {"text": "干扰项1", "is_correct": False, "language": "zh"},
                {"text": "干扰项2", "is_correct": False, "language": "zh"},
                {"text": "干扰项3", "is_correct": False, "language": "zh"},
            ],
            "explanation": "这是正确解析。"
        }
        all_pass_ratings = {
            "target_meaning": "pass",
            "unique_answer": "pass",
            "grammar": "pass",
            "naturalness": "pass",
            "explanation_consistency": "pass",
            "distractor_validity": "pass",
        }
        cand_checks = {
            "rule_checks": {"options_count": 4},
            "semantic_evaluations": [
                {"profile_version": "v1", "model": "eval-1", "ratings": all_pass_ratings, "rationale": "ok"},
                {"profile_version": "v1", "model": "eval-2", "ratings": all_pass_ratings, "rationale": "ok"}
            ],
            "result": "pass"
        }

        cand_resp = worker2.submit_candidate(
            item2["item_id"], item2["fencing_token"], item2["input_digest"],
            attempt_number=item2["attempt_number"], candidate_index=0,
            content=cand_content,
            generator_reservation_id=gen_res_id,
            evaluator_reservation_ids=[eval1_res_id, eval2_res_id],
            checks=cand_checks
        )
        assert cand_resp.status_code == 200, f"Submit candidate failed: {cand_resp.text}"
        cand_data = cand_resp.json()
        assert cand_data["disposition"] == "published", f"Expected published disposition, got {cand_data['disposition']}"
        assert cand_data["published_version_id"] is not None, "Expected published_version_id"

        # Verify in DB that question_version is published
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("SELECT status FROM question_version WHERE id = ?", (cand_data["published_version_id"],))
        qv_row = cur.fetchone()
        assert qv_row is not None and qv_row[0] == "published"
        conn.close()

        # 11. Worker 2 submits candidate with a reject rating -> disposition = rejected
        reject_checks = {
            "rule_checks": {"options_count": 4},
            "semantic_evaluations": [
                {"profile_version": "v1", "model": "eval-1", "ratings": {"target_meaning": "reject"}, "rationale": "wrong meaning"},
                {"profile_version": "v1", "model": "eval-2", "ratings": all_pass_ratings, "rationale": "ok"}
            ],
            "result": "reject"
        }
        cand2_resp = worker2.submit_candidate(
            item2["item_id"], item2["fencing_token"], item2["input_digest"],
            attempt_number=item2["attempt_number"], candidate_index=1,
            content=cand_content,
            generator_reservation_id=gen_res_id,
            evaluator_reservation_ids=[eval1_res_id, eval2_res_id],
            checks=reject_checks
        )
        assert cand2_resp.status_code == 200
        assert cand2_resp.json()["disposition"] == "rejected"
        assert cand2_resp.json().get("published_version_id") is None

        # 12. Worker 2 submits candidate with uncertain rating -> disposition = uncertain, quality_finding created
        uncertain_checks = {
            "rule_checks": {"options_count": 4},
            "semantic_evaluations": [
                {"profile_version": "v1", "model": "eval-1", "ratings": {"unique_answer": "uncertain"}, "rationale": "ambiguous"},
                {"profile_version": "v1", "model": "eval-2", "ratings": all_pass_ratings, "rationale": "ok"}
            ],
            "result": "uncertain"
        }
        cand3_resp = worker2.submit_candidate(
            item2["item_id"], item2["fencing_token"], item2["input_digest"],
            attempt_number=item2["attempt_number"], candidate_index=2,
            content=cand_content,
            generator_reservation_id=gen_res_id,
            evaluator_reservation_ids=[eval1_res_id, eval2_res_id],
            checks=uncertain_checks
        )
        assert cand3_resp.status_code == 200
        assert cand3_resp.json()["disposition"] == "uncertain"

        # Complete item
        comp_resp = worker2.complete(item2["item_id"], item2["fencing_token"], item2["input_digest"], outcome="completed")
        assert comp_resp.status_code == 200

        # 13. Browser check on /generation
        with sync_playwright() as p:
            browser = p.chromium.launch(headless=True)
            b_ctx = browser.new_context()
            b_ctx.tracing.start(screenshots=True, snapshots=True)
            page = b_ctx.new_page()
            page.goto(f"{base_url}/generation")

            assert "生成与质检管理" in page.title()
            cards = page.locator(".job-card")
            assert cards.count() >= 1

            screenshot_path = case_dir / "generation_browser.png"
            page.screenshot(path=str(screenshot_path))
            artifacts.append(str(screenshot_path))

            trace_path = case_dir / "trace.zip"
            b_ctx.tracing.stop(path=str(trace_path))
            artifacts.append(str(trace_path))
            browser.close()

        worker1.close()
        worker2.close()
        client.close()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "job_id": job_id,
            "published_version_id": cand_data["published_version_id"],
            "checks_verified": ["claim", "renew", "lease_takeover", "fence_rejection", "double_eval_publish", "reject_handling", "uncertain_handling"]
        }
    }
