"""W07-01: 处置记录与内容发布事实区分 (browser_worker)"""
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
    provider = ProviderServer(expected_api_key="w07-key-01")
    provider.start()

    try:
        with WebHarness(context, seed="core") as harness:
            base_url = harness.base_url
            page = harness.page
            data_dir = harness.data_dir
            db_path = data_dir / "app.db"

            # 1. Setup worker profile with uncertain scenario
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
                    "api_key": "w07-key-01",
                    "max_output_tokens": 1024,
                },
                "evaluator": {
                    "base_url": provider.base_url,
                    "model": "test-evaluator",
                    "api_key": "w07-key-01",
                    "max_output_tokens": 512,
                },
            }
            profile_path.write_text(json.dumps(profile, indent=2), encoding="utf-8")

            # 2. Get bank and sense from SQLite
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

            # 3. Create generation job with scenario uncertain
            provider.scenario_mode = "uncertain"
            provider.reset_counts()

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
            assert job_resp.status_code == 202, f"Job create failed: {job_resp.text}"
            job_id = job_resp.json()["job_id"]

            # 4. Run worker to evaluate candidate as uncertain and create finding
            res = run_worker(
                data_dir=data_dir,
                server_url=base_url,
                profile_path=profile_path,
                worker_id="worker-w07-01",
                max_items=1,
            )
            assert res.returncode == 0, f"Worker failed: {res.stderr}"

            # 5. Check finding created in SQLite
            conn = sqlite3.connect(db_path)
            cur = conn.cursor()
            cur.execute("SELECT id, candidate_id, status, finding_type, details_json, checker_version, created_at, updated_at FROM quality_finding WHERE job_id = ?", (job_id,))
            finding_row = cur.fetchone()
            assert finding_row is not None, "quality_finding should have been created for uncertain candidate"
            finding_id, candidate_id, finding_status, finding_type, details_json, checker_version, created_at, updated_at = finding_row
            assert finding_status == "open", f"Expected open status, got {finding_status}"

            # Create enough valid rows to force a second server cursor page.
            # The UI must consume next_cursor instead of silently stopping at 50.
            for offset in range(55):
                cur.execute(
                    """
                    INSERT INTO quality_finding (
                        id, job_id, candidate_id, finding_type, details_json,
                        checker_version, status, revision, created_at, updated_at
                    ) VALUES (?, ?, ?, ?, ?, ?, 'open', 0, ?, ?)
                    """,
                    (
                        str(uuid.uuid4()), job_id, candidate_id, finding_type,
                        details_json, checker_version, created_at - offset - 1,
                        updated_at - offset - 1,
                    ),
                )
            conn.commit()
            cur.execute("SELECT count(*) FROM quality_finding")
            expected_finding_count = cur.fetchone()[0]
            conn.close()

            # 6. Navigate to /quality in browser
            page.goto(f"{base_url}/quality", wait_until="networkidle")
            expect(page.locator("[data-testid='quality-view']")).to_be_visible()

            # Expect finding in table
            expect(page.locator(f"[data-testid='finding-id-{finding_id}']")).to_be_visible()
            expect(page.locator("[data-testid='findings-table']")).to_be_visible()
            expect(page.get_by_text(f"内容生成质检异常列表 ({expected_finding_count})")).to_be_visible()

            # 7. Click resolve button
            finding_row_locator = page.locator("tr").filter(
                has=page.locator(f"[data-testid='finding-id-{finding_id}']")
            )
            finding_row_locator.locator("[data-testid='resolve-finding-btn']").click()
            expect(page.locator("[data-testid='finding-detail-drawer']")).to_be_visible()
            expect(page.locator("[data-testid='finding-details-json']")).to_be_visible()
            rendered_details = page.locator("[data-testid='finding-details-json']").inner_text().strip()
            assert rendered_details, "Quality evidence must not render as an empty details block"
            parsed_details = json.loads(details_json)
            if isinstance(parsed_details, dict) and parsed_details:
                assert next(iter(parsed_details)) in rendered_details
            expect(page.locator("[data-testid='resolution-notice']")).to_be_visible()

            # 8. Submit resolution
            page.locator("[data-testid='resolution-reason-input']").fill("人工质检审核判定不合格，予以排除")
            page.locator("[data-testid='submit-resolution-btn']").click()

            # 9. Verify UI updates to resolution success card
            expect(page.locator("[data-testid='resolution-success-card']")).to_be_visible()
            expect(page.locator("[data-testid='resolution-success-card']")).to_contain_text("已记录处置")

            # 10. Verify SQLite database: resolution recorded, candidate NOT published
            conn = sqlite3.connect(db_path)
            cur = conn.cursor()
            cur.execute("SELECT action, reason FROM quality_finding_resolution WHERE finding_id = ?", (finding_id,))
            res_row = cur.fetchone()
            assert res_row is not None, "quality_finding_resolution record must exist"
            action, reason = res_row
            assert action == "exclude", f"Expected exclude, got {action}"

            cur.execute("SELECT status FROM quality_finding WHERE id = ?", (finding_id,))
            updated_status = cur.fetchone()[0]
            assert updated_status == "resolved", f"Expected resolved status, got {updated_status}"

            # Ensure candidate has NOT been published as question_version
            cur.execute("SELECT count(*) FROM question_version WHERE candidate_id = ?", (candidate_id,))
            pub_qv_count = cur.fetchone()[0]
            assert pub_qv_count == 0, f"Candidate must NOT be published as question_version, found {pub_qv_count}"

            cur.execute("SELECT disposition, published_version_id FROM question_candidate WHERE id = ?", (candidate_id,))
            cand_disp, pub_ver = cur.fetchone()
            assert cand_disp == "uncertain", f"Candidate disposition should remain uncertain, got {cand_disp}"
            assert pub_ver is None, f"Candidate should not have published_version_id, got {pub_ver}"
            conn.close()

            return {
                "status": "passed",
                "details": {
                    "finding_id": finding_id,
                    "candidate_id": candidate_id,
                    "resolution_action": action,
                    "disposition_recorded_in_db": True,
                    "candidate_not_published_verified": True,
                    "details_content_rendered": True,
                    "quality_cursor_pagination_count": expected_finding_count,
                },
            }
    finally:
        provider.stop()
