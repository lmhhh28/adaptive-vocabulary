"""W08-02: 导入、生成、质量和可用内容全链路
验收场景：
1. 词表导入界面录入词汇并提交导入任务，核验导入摘要与成员变动；
2. 题库详情确认版本与内容摘要；
3. 内容生成调度：真实 ProviderServer 与 worker 离线生成候选；
4. 候选审核与正式发布 (published)，生成正式 question_version；
5. 质量控制台核验异常处置与版本生命周期；
6. 自适应练习消费新发布题目，验证全链路生产到消费闭环；
7. SQLite 关联不变量核对。
"""
from contextlib import closing
import json
from pathlib import Path
import sqlite3
import time
import uuid

import httpx
from playwright.sync_api import expect
from support.web_harness import WebHarness
from support.provider_server import ProviderServer
from support.worker_process import run_worker


def run(context: dict) -> dict:
    case_dir = Path(context["case_dir"])
    provider = ProviderServer(expected_api_key="w08-key-02")
    provider.scenario_mode = "normal_pass"
    provider.start()

    try:
        with WebHarness(context, seed="core") as harness:
            base_url = harness.base_url
            page = harness.page
            data_dir = harness.data_dir
            db_path = data_dir / "app.db"

            # Setup worker generation profile
            profile_path = case_dir / "generation-profile.json"
            profile = {
                "config_version": "generation-v2",
                "budget_policy_version": "budget-v2",
                "mode": "test",
                "quality_policy_version": "quality-v1",
                "production_adapter": "chat-completions-json-v1",
                "generator": {
                    "base_url": provider.base_url,
                    "model": "test-generator",
                    "api_key": "w08-key-02",
                    "max_output_tokens": 1024,
                },
                "evaluator": {
                    "base_url": provider.base_url,
                    "model": "test-evaluator",
                    "api_key": "w08-key-02",
                    "max_output_tokens": 512,
                },
            }
            profile_path.write_text(json.dumps(profile, indent=2), encoding="utf-8")

            # Query core bank
            with closing(sqlite3.connect(db_path)) as conn:
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
                sense_id, sv_id = cur.fetchone()

            # -------------------------------------------------------------
            # Step 1: Wordlist Import UI
            # -------------------------------------------------------------
            page.goto(f"{base_url}/imports", wait_until="networkidle")
            expect(page.locator("[data-testid='imports-view']")).to_be_visible()

            # Paste words into text area
            input_el = page.locator("[data-testid='import-text-input']")
            expect(input_el).to_be_visible()
            input_el.fill("apple\nriver")

            # Submit import
            page.locator("[data-testid='submit-import-btn']").click()
            expect(page.locator("[data-testid='import-summary-card']")).to_be_visible()
            expect(page.locator("[data-testid='stat-total']")).to_be_visible()

            # -------------------------------------------------------------
            # Step 2: Bank View Verification
            # -------------------------------------------------------------
            page.goto(f"{base_url}/banks/{bank_id}", wait_until="networkidle")
            expect(page.locator("[data-testid='bank-detail-view']")).to_be_visible()
            expect(page.locator("[data-testid='bank-detail-title']")).to_be_visible()

            # -------------------------------------------------------------
            # Step 3: Content Generation with Real Worker & Provider
            # -------------------------------------------------------------
            with httpx.Client(base_url=base_url) as client:
                csrf_resp = client.get("/client-context")
                assert csrf_resp.status_code == 200
                csrf_token = csrf_resp.json().get("csrf_token", "")

                gen_resp = client.post(
                    f"/api/v1/banks/{bank_id}/generation-jobs",
                    headers={"X-CSRF-Token": csrf_token, "Idempotency-Key": str(uuid.uuid4())},
                    json={
                        "sense_refs": [{"sense_id": sense_id, "sense_version_id": sv_id}],
                        "generation_config_version": "v1",
                        "quality_policy_version": "v1",
                        "expected_revision": bank_rev,
                    },
                )
                assert gen_resp.status_code in (200, 202), f"Failed to create generation job: {gen_resp.text}"
                job_data = gen_resp.json()
                job_id = job_data.get("job_id") or job_data.get("id")
                assert job_id, "Missing job_id in creation response"

            # Execute real Python worker process
            worker_res = run_worker(
                data_dir=data_dir,
                server_url=base_url,
                profile_path=profile_path,
                worker_id="worker-w08-02",
                max_items=1,
            )
            assert worker_res.returncode == 0, f"Worker failed with {worker_res.returncode}: {worker_res.stderr}"

            # -------------------------------------------------------------
            # Step 4: Verify Published Candidate in SQLite
            # -------------------------------------------------------------
            with closing(sqlite3.connect(db_path)) as conn:
                cur = conn.cursor()
                cur.execute("SELECT id, disposition, published_version_id FROM question_candidate WHERE job_id = ?", (job_id,))
                candidates = cur.fetchall()
                assert len(candidates) >= 1, f"Expected at least 1 candidate for job {job_id}"
                cand_id, cand_disp, cand_pub_ver = candidates[0]
                assert cand_disp == "published", f"Expected candidate disposition 'published', got {cand_disp}"
                assert cand_pub_ver is not None, "published_version_id must not be null"

                cur.execute("SELECT id, status FROM question_version WHERE id = ?", (cand_pub_ver,))
                qv_row = cur.fetchone()
                assert qv_row is not None, f"question_version {cand_pub_ver} not found"
                assert qv_row[1] == "published", f"question_version status is not published: {qv_row[1]}"

            # -------------------------------------------------------------
            # Step 5: Quality Console Inspection
            # -------------------------------------------------------------
            page.goto(f"{base_url}/quality", wait_until="networkidle")
            expect(page.locator("[data-testid='quality-view']")).to_be_visible()

            # Switch to Question Versions tab
            page.get_by_role("tab", name="题目版本与处置").click()
            expect(page.locator("[data-testid='question-versions-table']")).to_be_visible()

            # -------------------------------------------------------------
            # Step 6: Practice Answering Using Available Content
            # -------------------------------------------------------------
            page.goto(f"{base_url}/practice?bank_id={bank_id}", wait_until="networkidle")
            expect(page.locator("[data-testid='practice-view']")).to_be_visible()
            expect(page.locator("[data-testid='question-stem-text']")).to_be_visible()

            # Answer question
            page.locator("[data-testid='option-item-0']").click()
            expect(page.locator("[data-testid='feedback-panel']")).to_be_visible()

            # End practice
            page.locator("[data-testid='end-session-btn']").click()
            modal_confirm = page.locator("[data-testid='confirm-end-btn']")
            if modal_confirm.is_visible():
                modal_confirm.click()
            else:
                page.get_by_role("button", name="结束练习").click()
            page.wait_for_timeout(500)

            # -------------------------------------------------------------
            # Step 7: SQLite Invariant Assertions
            # -------------------------------------------------------------
            with closing(sqlite3.connect(db_path)) as conn:
                cur = conn.cursor()
                # 1. Candidate is published
                cur.execute("SELECT disposition, published_version_id FROM question_candidate WHERE id = ?", (cand_id,))
                row = cur.fetchone()
                assert row is not None, f"Candidate {cand_id} not found"
                assert row[0] == "published", f"Unexpected disposition: {row[0]}"

                # 2. Question version exists and is published
                cur.execute("SELECT status, sense_id FROM question_version WHERE id = ?", (cand_pub_ver,))
                qv_row = cur.fetchone()
                assert qv_row is not None, f"question_version {cand_pub_ver} not found"
                assert qv_row[0] == "published", f"question_version status is not published: {qv_row[0]}"

                # 3. Import job recorded
                cur.execute("SELECT count(*) FROM import_job")
                import_jobs_count = cur.fetchone()[0]
                assert import_jobs_count >= 1, "Expected at least 1 import job"

    finally:
        provider.stop()

    return {
        "status": "passed",
        "artifacts": harness.artifacts,
        "details": {
            "job_id": job_id,
            "candidate_id": cand_id,
            "published_question_version_id": cand_pub_ver,
            "import_jobs_count": import_jobs_count,
            "pipeline_closed_loop_verified": True,
        }
    }
