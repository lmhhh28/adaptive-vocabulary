"""W07-03: 重建成功与HTTP200业务失败 (browser)"""
import sqlite3
import httpx
from playwright.sync_api import expect
from support.web_harness import WebHarness


def run(context: dict) -> dict:
    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page
        data_dir = harness.data_dir
        db_path = data_dir / "app.db"

        # -------------------------------------------------------------
        # Part 1: Evidence disposition triggers successful rebuild
        # -------------------------------------------------------------
        page.goto(f"{base_url}/quality", wait_until="networkidle")
        expect(page.locator("[data-testid='quality-view']")).to_be_visible()

        # Switch to Question Versions tab
        page.locator("[data-testid='tab-question-versions']").click()
        expect(page.locator("[data-testid='question-versions-table']")).to_be_visible()

        # Click Evidence Disposition on first row
        first_row = page.locator("[data-testid='question-versions-table'] tbody tr.ant-table-row").first
        first_row.locator("[data-testid='disposition-btn']").click()

        expect(page.locator("[data-testid='disposition-modal-content']")).to_be_visible()
        page.locator("[data-testid='disposition-reason-input']").fill("专家核验标记证据争议并触发同步重建")
        page.locator("[data-testid='submit-disposition-btn']").click()

        # Verify disposition result shows succeeded
        expect(page.locator("[data-testid='disposition-result']")).to_be_visible()
        expect(page.locator("[data-testid='disposition-result']")).to_contain_text("succeeded")

        # Verify SQLite model_rebuild_run status
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("SELECT status FROM model_rebuild_run ORDER BY created_at DESC LIMIT 1")
        last_rebuild_status = cur.fetchone()[0]
        assert last_rebuild_status == "succeeded", f"Expected succeeded rebuild run, got {last_rebuild_status}"
        conn.close()

        # Close disposition modal
        page.keyboard.press("Escape")

        # -------------------------------------------------------------
        # Part 2: Simulate Rebuild Failed -> HTTP 200 but Business Failed
        # -------------------------------------------------------------
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        failed_run_id = "failed-run-001"
        cur.execute("""
            INSERT OR REPLACE INTO model_rebuild_run (
                id, kind, base_active_version, target_projection_version, target_policy,
                base_revision_digest, fixed_watermark, status, requires_rebuild, created_at, updated_at
            ) VALUES (?, 'evidence_revision', 'v1', 'v_fail', 'policy-v2', 'digest123', 10, 'failed', 1, 1000, 1000)
        """, (failed_run_id,))
        cur.execute("""
            UPDATE projection_control
            SET mode = 'rebuilding', run_id = ?, run_status = 'failed', requires_rebuild = 1
            WHERE id = 1
        """, (failed_run_id,))
        conn.commit()
        conn.close()

        # Verify HTTP status is 200 OK from server API
        client = httpx.Client(base_url=base_url)
        maint_resp = client.get("/api/v1/maintenance")
        assert maint_resp.status_code == 200, f"Expected HTTP 200, got {maint_resp.status_code}"
        maint_body = maint_resp.json()
        assert maint_body["run_status"] == "failed", f"Expected run_status=failed, got {maint_body.get('run_status')}"
        assert maint_body["next_action"] == "resume_or_abort", f"Expected next_action=resume_or_abort, got {maint_body.get('next_action')}"

        # Switch to maintenance tab in browser
        page.locator("[data-testid='tab-maintenance']").click()
        expect(page.locator("[data-testid='maintenance-info']")).to_be_visible()

        # Check maintenance info displays failed status and resume_or_abort next action
        expect(page.locator("[data-testid='maint-run-status']")).to_contain_text("failed")
        expect(page.locator("[data-testid='maint-next-action']")).to_contain_text("resume_or_abort")

        # Prominent business failure alert must be visible
        expect(page.locator("[data-testid='maintenance-status-failed']")).to_be_visible()
        expect(page.locator("[data-testid='maintenance-status-failed']")).to_contain_text("业务失败")
        expect(page.locator("[data-testid='maintenance-status-failed']")).to_contain_text("resume_or_abort")

        # Ensure success alert is NOT rendered
        expect(page.locator("[data-testid='maintenance-status-ok']")).not_to_be_visible()

        return {
            "status": "passed",
            "details": {
                "disposition_rebuild_success_verified": True,
                "http_status_code": maint_resp.status_code,
                "business_status": maint_body["run_status"],
                "business_failure_alert_rendered": True,
            },
        }
