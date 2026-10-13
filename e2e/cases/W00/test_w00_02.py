"""W00-02: 真实 context、CSRF、维护与活动会话"""
from contextlib import closing
import json
from pathlib import Path
import sqlite3
import httpx
from playwright.sync_api import expect
from support.web_harness import WebHarness


def run(context: dict) -> dict:
    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page

        # 1. Access page to initialize context
        page.goto(base_url, wait_until="networkidle")

        # 2. Check cookies in browser context
        cookies = page.context.cookies()
        session_cookie = next((c for c in cookies if c["name"] == "avocab_session"), None)
        assert session_cookie, "Expected avocab_session cookie to be set by /client-context"

        # 3. Verify CSRF validation via HTTP client sharing same cookie
        with httpx.Client(cookies={"avocab_session": session_cookie["value"]}) as client:
            # 3.1 Fetch context
            ctx_resp = client.get(f"{base_url}/client-context")
            assert ctx_resp.status_code == 200
            csrf_token = ctx_resp.json().get("csrf_token")
            assert csrf_token, "Expected valid csrf_token"

            # 3.2 Post without CSRF token -> rejected (403)
            no_csrf = client.post(f"{base_url}/api/v1/test/action", json={"test": True})
            assert no_csrf.status_code == 403, f"Expected 403 without CSRF, got {no_csrf.status_code}"

            # 3.3 Post with correct CSRF token -> success (200)
            with_csrf = client.post(
                f"{base_url}/api/v1/test/action",
                headers={"X-CSRF-Token": csrf_token},
                json={"test": True},
            )
            assert with_csrf.status_code == 200, f"Expected 200 with CSRF, got {with_csrf.status_code}"

        # 4. Inject maintenance mode into DB (projection_control.mode = 'rebuilding', id = 1)
        db_path = harness.data_dir / "app.db"
        with closing(sqlite3.connect(db_path)) as conn, conn:
            conn.execute("UPDATE projection_control SET mode = 'rebuilding', requires_rebuild = 1 WHERE id = 1")

        # 5. Reload browser page and check maintenance banner
        page.reload(wait_until="networkidle")
        expect(page.locator("[data-testid='maintenance-banner']")).to_be_visible()
        expect(page.locator("[data-testid='maintenance-banner']")).to_contain_text("系统处于维护模式")

        # 6. Verify write operations are blocked by maintenance gate middleware
        with httpx.Client(cookies={"avocab_session": session_cookie["value"]}) as client:
            blocked_resp = client.post(
                f"{base_url}/api/v1/test/action",
                headers={"X-CSRF-Token": csrf_token},
                json={"test": True},
            )
            assert blocked_resp.status_code == 503, f"Expected 503 in maintenance mode, got {blocked_resp.status_code}"
            err = blocked_resp.json().get("error", {})
            assert err.get("code") in ("MAINTENANCE", "MAINTENANCE_MODE"), f"Expected MAINTENANCE code, got {err}"

        # 7. Restore active mode
        with closing(sqlite3.connect(db_path)) as conn, conn:
            conn.execute("UPDATE projection_control SET mode = 'active', requires_rebuild = 0 WHERE id = 1")

        page.reload(wait_until="networkidle")
        expect(page.locator("[data-testid='maintenance-banner']")).not_to_be_visible()

        return {
            "status": "passed",
            "artifacts": harness.artifacts,
            "details": {
                "session_id_prefix": session_cookie["value"][:8],
                "csrf_validated": True,
                "maintenance_gate_verified": True,
            },
        }
