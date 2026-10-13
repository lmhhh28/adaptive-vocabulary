"""W06-03: worker恢复与维护等待"""
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
    provider = ProviderServer(expected_api_key="w06-key-03", scenario_mode="normal_pass")
    provider.start()

    try:
        with WebHarness(context, seed="core") as harness:
            base_url = harness.base_url
            page = harness.page
            data_dir = harness.data_dir
            db_path = data_dir / "app.db"

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
                    "api_key": "w06-key-03",
                    "max_output_tokens": 1024,
                },
                "evaluator": {
                    "base_url": provider.base_url,
                    "model": "test-evaluator",
                    "api_key": "w06-key-03",
                    "max_output_tokens": 512,
                },
            }
            profile_path.write_text(json.dumps(profile, indent=2), encoding="utf-8")

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
            sense_id, sense_ver = cur.fetchone()
            conn.close()

            # 1. Create generation job
            client = httpx.Client(base_url=base_url)
            csrf_resp = client.get("/client-context")
            csrf_token = csrf_resp.json().get("csrf_token", "")

            job_resp = client.post(
                f"/api/v1/banks/{bank_id}/generation-jobs",
                headers={"X-CSRF-Token": csrf_token, "Idempotency-Key": str(uuid.uuid4())},
                json={
                    "sense_refs": [{"sense_id": sense_id, "sense_version_id": sense_ver}],
                    "generation_config_version": "v1",
                    "quality_policy_version": "v1",
                    "expected_revision": bank_rev,
                },
            )
            assert job_resp.status_code == 202
            job_id = job_resp.json()["job_id"]

            # 2. Worker 1 runs with crash_after_gen=True
            res1 = run_worker(
                data_dir=data_dir,
                server_url=base_url,
                profile_path=profile_path,
                worker_id="worker-crash-w06",
                max_items=1,
                crash_after_gen=True,
            )
            assert res1.returncode != 0, f"Worker 1 should have crashed, returned {res1.returncode}"

            # 3. Simulate lease expiration in SQLite
            conn = sqlite3.connect(db_path)
            cur = conn.cursor()
            cur.execute("UPDATE generation_item SET lease_until = lease_until - 300000000 WHERE job_id = ?", (job_id,))
            conn.commit()
            conn.close()

            # 4. Worker 2 claims item -> resumes from checkpoint, finishes generation & evaluation
            res2 = run_worker(
                data_dir=data_dir,
                server_url=base_url,
                profile_path=profile_path,
                worker_id="worker-recovery-w06",
                max_items=1,
            )
            assert res2.returncode == 0, f"Worker 2 failed with code {res2.returncode}:\n{res2.stderr}"

            # 5. Verify published candidate in SQLite
            conn = sqlite3.connect(db_path)
            cur = conn.cursor()
            cur.execute("SELECT id, disposition, published_version_id FROM question_candidate WHERE job_id = ?", (job_id,))
            cand_row = cur.fetchone()
            assert cand_row is not None, "Candidate not found after recovery"
            cand_id, cand_disp, cand_pub_ver = cand_row
            assert cand_disp == "published", f"Expected published after recovery, got {cand_disp}"
            assert cand_pub_ver is not None, "Candidate should have published_version_id"

            # 6. Also add a candidate with pending_maintenance to test maintenance waiting UI
            cur.execute("SELECT id FROM generation_item WHERE job_id = ?", (job_id,))
            item_id = cur.fetchone()[0]
            now_ts = 1757680000000000
            cur.execute("""
                INSERT INTO question_candidate (id, item_id, job_id, attempt_number, candidate_index, content_json, generation_metadata_json, checks_json, disposition, created_at, updated_at)
                VALUES ('cand-maint-01', ?, ?, 2, 1, '{"stem":"Maintenance pending test stem","options":[{"text":"Option A","is_correct":true}]}', '{}', '{}', 'pending_maintenance', ?, ?)
            """, (item_id, job_id, now_ts, now_ts))
            conn.commit()
            conn.close()

            # 7. Verify in Browser UI
            page.goto(f"{base_url}/generation", wait_until="networkidle")
            expect(page.locator("[data-testid='generation-view']")).to_be_visible()

            # Open Job Detail
            page.locator(f"[data-testid='view-job-btn-{job_id}']").click()
            expect(page.locator("[data-testid='job-detail-drawer']")).to_be_visible()

            # Verify both recovered published candidate and pending_maintenance candidate
            expect(page.locator(f"[data-testid='candidate-disposition-{cand_id}']")).to_contain_text("已发布")
            expect(page.locator("[data-testid='candidate-disposition-cand-maint-01']")).to_contain_text("待维护")

            return {
                "status": "passed",
                "details": {
                    "worker_crash_simulated": True,
                    "lease_expiry_and_worker_recovery_verified": True,
                    "checkpoint_resume_and_publish_verified": True,
                    "pending_maintenance_rendered_in_ui": True,
                },
            }
    finally:
        provider.stop()
