"""W06-02: 真实worker正负候选与正式发布事实"""
import json
from pathlib import Path
import sqlite3
import uuid
import httpx
from playwright.sync_api import expect
from support.web_harness import WebHarness
from support.provider_server import ProviderServer
from support.worker_process import run_worker


def run(context: dict) -> dict:
    case_dir = Path(context["case_dir"])
    provider = ProviderServer(expected_api_key="w06-key-02")
    provider.start()

    try:
        with WebHarness(context, seed="core") as harness:
            base_url = harness.base_url
            page = harness.page
            data_dir = harness.data_dir
            db_path = data_dir / "app.db"

            # Setup worker profile
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
                    "api_key": "w06-key-02",
                    "max_output_tokens": 1024,
                },
                "evaluator": {
                    "base_url": provider.base_url,
                    "model": "test-evaluator",
                    "api_key": "w06-key-02",
                    "max_output_tokens": 512,
                },
            }
            profile_path.write_text(json.dumps(profile, indent=2), encoding="utf-8")

            # Get bank and senses from SQLite
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
                LIMIT 2
            """, (bank_id,))
            senses = cur.fetchall()
            sense1_id, sense1_ver = senses[0]
            sense2_id, sense2_ver = senses[1]
            conn.close()

            # -------------------------------------------------------------
            # 1. Job 1: Normal pass -> Published candidate
            # -------------------------------------------------------------
            provider.scenario_mode = "normal_pass"
            provider.reset_counts()

            # Create job 1
            client = httpx.Client(base_url=base_url)
            csrf_resp = client.get("/client-context")
            csrf_token = csrf_resp.json().get("csrf_token", "")

            job1_resp = client.post(
                f"/api/v1/banks/{bank_id}/generation-jobs",
                headers={"X-CSRF-Token": csrf_token, "Idempotency-Key": str(uuid.uuid4())},
                json={
                    "sense_refs": [{"sense_id": sense1_id, "sense_version_id": sense1_ver}],
                    "generation_config_version": "v1",
                    "quality_policy_version": "v1",
                    "expected_revision": bank_rev,
                },
            )
            assert job1_resp.status_code == 202, f"Job 1 create failed: {job1_resp.text}"
            job1_id = job1_resp.json()["job_id"]

            # Run worker for Job 1
            res1 = run_worker(
                data_dir=data_dir,
                server_url=base_url,
                profile_path=profile_path,
                worker_id="worker-w06-02-a",
                max_items=1,
            )
            assert res1.returncode == 0, f"Worker 1 failed with {res1.returncode}: {res1.stderr}"

            # Verify Job 1 in SQLite: published disposition and published_version_id
            conn = sqlite3.connect(db_path)
            cur = conn.cursor()
            cur.execute("SELECT id, disposition, published_version_id FROM question_candidate WHERE job_id = ?", (job1_id,))
            cand1_row = cur.fetchone()
            assert cand1_row is not None, "Candidate 1 not found"
            cand1_id, cand1_disp, cand1_pub_ver = cand1_row
            assert cand1_disp == "published", f"Expected published, got {cand1_disp}"
            assert cand1_pub_ver is not None, "Candidate 1 should have published_version_id"

            cur.execute("SELECT id FROM question_version WHERE id = ?", (cand1_pub_ver,))
            assert cur.fetchone() is not None, "Formal question_version must exist"

            # -------------------------------------------------------------
            # 2. Job 2: Always reject -> Rejected candidate
            # -------------------------------------------------------------
            cur.execute("SELECT revision FROM bank WHERE id = ?", (bank_id,))
            bank_rev2 = cur.fetchone()[0]
            conn.close()

            provider.scenario_mode = "always_reject"
            provider.reset_counts()

            job2_resp = client.post(
                f"/api/v1/banks/{bank_id}/generation-jobs",
                headers={"X-CSRF-Token": csrf_token, "Idempotency-Key": str(uuid.uuid4())},
                json={
                    "sense_refs": [{"sense_id": sense2_id, "sense_version_id": sense2_ver}],
                    "generation_config_version": "v1",
                    "quality_policy_version": "v1",
                    "expected_revision": bank_rev2,
                },
            )
            assert job2_resp.status_code == 202, f"Job 2 create failed: {job2_resp.text}"
            job2_id = job2_resp.json()["job_id"]

            # Run worker for Job 2
            res2 = run_worker(
                data_dir=data_dir,
                server_url=base_url,
                profile_path=profile_path,
                worker_id="worker-w06-02-b",
                max_items=1,
            )
            assert res2.returncode == 0, f"Worker 2 failed with {res2.returncode}: {res2.stderr}"

            # Verify Job 2 in SQLite: rejected disposition, published_count = 0, rejected_count = 1
            conn = sqlite3.connect(db_path)
            cur = conn.cursor()
            cur.execute("SELECT id, disposition, published_version_id FROM question_candidate WHERE job_id = ?", (job2_id,))
            cand2_row = cur.fetchone()
            assert cand2_row is not None, "Candidate 2 not found"
            cand2_id, cand2_disp, cand2_pub_ver = cand2_row
            assert cand2_disp == "rejected", f"Expected rejected, got {cand2_disp}"
            assert cand2_pub_ver is None, "Rejected candidate must not have published_version_id"

            cur.execute("SELECT published_count, rejected_count FROM generation_job WHERE id = ?", (job2_id,))
            pub_cnt2, rej_cnt2 = cur.fetchone()
            assert pub_cnt2 == 0, "Job 2 published_count must be 0"
            assert rej_cnt2 == 1, "Job 2 rejected_count must be 1"
            conn.close()

            # -------------------------------------------------------------
            # 3. Verify in Browser UI
            # -------------------------------------------------------------
            page.goto(f"{base_url}/generation", wait_until="networkidle")
            expect(page.locator("[data-testid='generation-view']")).to_be_visible()

            # Inspect Job 1 in UI
            page.locator(f"[data-testid='view-job-btn-{job1_id}']").click()
            expect(page.locator("[data-testid='job-detail-drawer']")).to_be_visible()
            expect(page.locator(f"[data-testid='candidate-disposition-{cand1_id}']")).to_contain_text("已发布")

            page.locator(f"[data-testid='inspect-candidate-btn-{cand1_id}']").click()
            expect(page.locator("[data-testid='candidate-detail-modal']")).to_be_visible()
            expect(page.locator("[data-testid='candidate-stem']")).to_be_visible()
            expect(page.locator("[data-testid='candidate-options']")).to_be_visible()

            # Close candidate modal and drawer
            page.locator(".ant-modal-close").click()
            expect(page.locator("[data-testid='candidate-detail-modal']")).not_to_be_visible()
            page.locator(".ant-drawer-close").click()
            expect(page.locator("[data-testid='job-detail-drawer']")).not_to_be_visible()

            # Inspect Job 2 in UI
            page.locator(f"[data-testid='view-job-btn-{job2_id}']").click()
            expect(page.locator("[data-testid='job-detail-drawer']")).to_be_visible()
            expect(page.locator(f"[data-testid='candidate-disposition-{cand2_id}']")).to_contain_text("已拒绝")

            page.locator(f"[data-testid='inspect-candidate-btn-{cand2_id}']").click()
            expect(page.locator("[data-testid='candidate-detail-modal']")).to_be_visible()

            return {
                "status": "passed",
                "details": {
                    "job1_published_verified": True,
                    "job2_rejected_verified": True,
                    "formal_question_version_verified": True,
                    "completed_not_all_published_verified": True,
                    "browser_ui_inspections_verified": True,
                },
            }
    finally:
        provider.stop()
