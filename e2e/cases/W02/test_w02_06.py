"""W02-06: 无候选、冲突、维护和结束边界"""
import sqlite3
from contextlib import closing
from playwright.sync_api import expect
from support.http_client import APIClient
from support.web_harness import WebHarness


def run(context: dict) -> dict:
    bank_id = "00000000-0000-4000-8000-000000000801"
    other_bank_id = "00000000-0000-4000-8000-000000000802"
    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page
        db_path = harness.data_dir / "app.db"

        # -------------------------------------------------------------
        # 1. Test End Session Flow (PR-13)
        # -------------------------------------------------------------
        page.goto(f"{base_url}/practice?bank_id={bank_id}", wait_until="networkidle")
        expect(page.locator("[data-testid='practice-view']")).to_be_visible()

        # Answer question 0
        page.locator("[data-testid='option-item-0']").click()
        expect(page.locator("[data-testid='feedback-panel']")).to_be_visible()

        # Click End Session button
        page.locator("[data-testid='end-session-btn']").click()

        # Modal opens - check dialog content
        expect(page.get_by_text("结束本轮练习？")).to_be_visible()

        # Confirm End by clicking the danger confirm button
        page.locator("[data-testid='confirm-end-btn']").click()
        expect(page.locator("[data-testid='practice-ended-summary']")).to_be_visible()

        # Verify in DB that session status is 'ended'
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT status FROM session ORDER BY created_at DESC LIMIT 1")
            status = cur.fetchone()[0]
            assert status == "ended", f"Expected session status to be 'ended', got {status}"

        # -------------------------------------------------------------
        # 2. Test Active Session Conflict
        # -------------------------------------------------------------
        # Create an active session on bank 801 via API client
        client = APIClient(base_url)
        client.initialize_client_context()
        create_res = client.post("/api/v1/practice-sessions", json={"bank_id": bank_id})
        assert create_res.status_code == 201
        active_sid = create_res.json()["session"]["id"]

        # Now in the browser, attempt to create a session on a DIFFERENT bank (802)
        page.goto(f"{base_url}/practice?bank_id={other_bank_id}", wait_until="networkidle")

        # Must display active conflict alert
        expect(page.locator("[data-testid='active-conflict-alert']")).to_be_visible()
        expect(page.locator("[data-testid='active-conflict-alert']")).to_contain_text("存在进行中的作答会话")

        # Resume conflict session
        page.locator("[data-testid='resume-conflict-btn']").click()
        page.wait_for_timeout(500)
        expect(page.locator("[data-testid='practice-view']")).to_be_visible()

        # Query-parameter route changes reuse the same hook instance. Moving
        # from an explicit session URL to /practice must not leave the mounted
        # guard false and strand the page on its skeleton.
        page.goto(f"{base_url}/practice?session_id={active_sid}", wait_until="networkidle")
        expect(page.locator("[data-testid='question-stem-text']")).to_be_visible()
        page.get_by_role("menuitem", name="自适应练习").click()
        expect(page.locator("[data-testid='question-stem-text']")).to_be_visible()
        expect(page.locator(".ant-skeleton")).to_have_count(0)

        # -------------------------------------------------------------
        # 3. Test Maintenance Gate
        # -------------------------------------------------------------
        with closing(sqlite3.connect(db_path)) as conn, conn:
            conn.execute("UPDATE projection_control SET mode = 'rebuilding', requires_rebuild = 1 WHERE id = 1")

        page.reload(wait_until="networkidle")
        expect(page.locator("[data-testid='maintenance-banner']")).to_be_visible()
        expect(page.locator("[data-testid='maintenance-banner']")).to_contain_text("系统处于维护模式")

        # Restore active mode
        with closing(sqlite3.connect(db_path)) as conn, conn:
            conn.execute("UPDATE projection_control SET mode = 'active', requires_rebuild = 0 WHERE id = 1")

        page.reload(wait_until="networkidle")
        expect(page.locator("[data-testid='maintenance-banner']")).not_to_be_visible()

        # -------------------------------------------------------------
        # 4. Test No Candidates UI State
        # -------------------------------------------------------------
        # End active_sid
        client.post(
            f"/api/v1/practice-sessions/{active_sid}/end",
            json={"expected_revision": 0, "reason": "ended"},
        )

        # Intercept next endpoint with the real discriminated-union status used
        # by the backend when no candidate is currently eligible.
        page.route(
            "**/api/v1/practice-sessions/*/next",
            lambda route: route.fulfill(
                status=200,
                content_type="application/json",
                body='{"status":"unavailable","reason":"NO_ELIGIBLE_QUESTION","counts":{"cooldown":1,"content_missing":0,"exhausted":0}}',
            ),
        )

        page.goto(f"{base_url}/practice?bank_id={bank_id}", wait_until="networkidle")
        expect(page.locator("[data-testid='no-candidates-alert']")).to_be_visible()
        expect(page.locator("[data-testid='no-candidates-alert']")).to_contain_text("本轮练习暂无可作答题目")

        return {
            "status": "passed",
            "artifacts": harness.artifacts,
            "details": {
                "end_session_flow_verified": True,
                "db_status_ended": True,
                "conflict_alert_and_resume_verified": True,
                "route_change_recovery_verified": True,
                "maintenance_gate_verified": True,
                "no_candidates_boundary_verified": True,
            },
        }
