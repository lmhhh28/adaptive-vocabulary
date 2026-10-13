"""W07-04: 活动会话保护和维护禁写 (browser)"""
import sqlite3
from playwright.sync_api import expect
from support.http_client import APIClient
from support.web_harness import WebHarness


def run(context: dict) -> dict:
    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page
        data_dir = harness.data_dir
        db_path = data_dir / "app.db"

        # Connect to SQLite to find bank ID and a question_version
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("SELECT id FROM bank WHERE name = '开发样例'")
        bank_id = cur.fetchone()[0]

        cur.execute("SELECT id FROM question_version WHERE status = 'published' LIMIT 1")
        sample_qv_id = cur.fetchone()[0]
        conn.close()

        # -------------------------------------------------------------
        # Part 1: Active Session Protection
        # -------------------------------------------------------------
        client = APIClient(base_url)
        client.initialize_client_context()

        # Start practice session
        create_resp = client.post("/api/v1/practice-sessions", json={"bank_id": bank_id})
        assert create_resp.status_code == 201
        sess_data = create_resp.json()["session"]
        sess_id = sess_data["id"]
        sess_rev = sess_data["revision"]

        # Open /quality in browser
        page.goto(f"{base_url}/quality", wait_until="networkidle")
        expect(page.locator("[data-testid='quality-view']")).to_be_visible()

        # Active session alert must be visible
        expect(page.locator("[data-testid='active-session-alert']")).to_be_visible()
        expect(page.locator("[data-testid='active-session-alert']")).to_contain_text("存在活动作答会话")

        # Verify backend API returns 409 SESSION_ACTIVE
        retire_resp = client.post(f"/api/v1/question-versions/{sample_qv_id}/retire", json={"reason": "test in active session"})
        assert retire_resp.status_code == 409, f"Expected 409 SESSION_ACTIVE, got {retire_resp.status_code}"
        assert retire_resp.json().get("error", {}).get("code") == "SESSION_ACTIVE"

        disp_resp = client.post(
            f"/api/v1/question-versions/{sample_qv_id}/evidence-dispositions",
            json={"action": "disputed", "reason": "test in active session"},
        )
        assert disp_resp.status_code == 409, f"Expected 409 SESSION_ACTIVE, got {disp_resp.status_code}"
        assert disp_resp.json().get("error", {}).get("code") == "SESSION_ACTIVE"

        content_resp = client.get("/api/v1/content/question-versions")
        assert content_resp.status_code == 409, f"Expected 409 SESSION_ACTIVE, got {content_resp.status_code}"
        assert content_resp.json().get("error", {}).get("code") == "SESSION_ACTIVE"

        # Switch to question versions tab, action buttons should be disabled
        page.locator("[data-testid='tab-question-versions']").click()
        first_retire_btn = page.locator("[data-testid='retire-btn']").first
        if first_retire_btn.is_visible():
            expect(first_retire_btn).to_be_disabled()

        # -------------------------------------------------------------
        # Part 2: Maintenance Write Block
        # -------------------------------------------------------------
        # End practice session cleanly
        client.post(
            f"/api/v1/practice-sessions/{sess_id}/end",
            json={"expected_revision": sess_rev, "reason": "ended"},
            headers={"Idempotency-Key": f"end-{sess_id}"},
        )
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("UPDATE session SET status = 'ended' WHERE id = ?", (sess_id,))
        # Set system to rebuilding mode in SQLite
        cur.execute("UPDATE projection_control SET mode = 'rebuilding', requires_rebuild = 1 WHERE id = 1")
        conn.commit()
        conn.close()

        # Reload /quality page
        page.reload(wait_until="networkidle")
        expect(page.locator("[data-testid='quality-view']")).to_be_visible()

        # Maintenance alert must be visible
        expect(page.locator("[data-testid='maintenance-alert']")).to_be_visible()
        expect(page.locator("[data-testid='maintenance-alert']")).to_contain_text("系统处于维护重建模式")

        # Verify backend API returns 503 MAINTENANCE
        maint_retire = client.post(f"/api/v1/question-versions/{sample_qv_id}/retire", json={"reason": "test in maintenance"})
        assert maint_retire.status_code == 503, f"Expected 503 MAINTENANCE, got {maint_retire.status_code}"
        assert maint_retire.json().get("error", {}).get("code") == "MAINTENANCE"

        maint_disp = client.post(
            f"/api/v1/question-versions/{sample_qv_id}/evidence-dispositions",
            json={"action": "disputed", "reason": "test in maintenance"},
        )
        assert maint_disp.status_code == 503, f"Expected 503 MAINTENANCE, got {maint_disp.status_code}"
        assert maint_disp.json().get("error", {}).get("code") == "MAINTENANCE"

        return {
            "status": "passed",
            "details": {
                "active_session_409_boundary_verified": True,
                "active_session_alert_rendered": True,
                "maintenance_503_write_block_verified": True,
                "maintenance_alert_rendered": True,
            },
        }
