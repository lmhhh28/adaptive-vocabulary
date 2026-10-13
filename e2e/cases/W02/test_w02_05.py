"""W02-05: 真实已接受响应丢失后的刷新收敛"""
import sqlite3
from contextlib import closing
from playwright.sync_api import expect
from support.web_harness import WebHarness


def run(context: dict) -> dict:
    bank_id = "00000000-0000-4000-8000-000000000801"
    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page
        db_path = harness.data_dir / "app.db"

        # 1. Open practice page
        page.goto(f"{base_url}/practice?bank_id={bank_id}", wait_until="networkidle")
        expect(page.locator("[data-testid='practice-view']")).to_be_visible()
        expect(page.locator("[data-testid='question-stem-text']")).to_be_visible()

        # 2. Intercept attempts endpoint: forward request to backend so server processes it,
        # but abort the response to simulate packet loss on the way back to the browser
        intercepted = False

        def handle_attempts_route(route, request):
            nonlocal intercepted
            intercepted = True
            # Let server process it via fetch
            _ = route.fetch()
            # Then abort delivery to client
            route.abort("connectionreset")

        page.route("**/api/v1/practice-sessions/*/attempts", handle_attempts_route)

        # 3. Click option 0 to answer
        page.locator("[data-testid='option-item-0']").click()

        # Wait for the abort to take effect and UI to handle the network failure
        page.wait_for_timeout(1000)
        assert intercepted, "Expected attempt route to be intercepted"

        # Check DB: Server DID receive and accept the attempt in attempt_event!
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT count(*) FROM attempt_event")
            attempt_count_before = cur.fetchone()[0]
            assert attempt_count_before == 1, f"Expected 1 accepted attempt in DB, got {attempt_count_before}"

        # 4. Retry the exact original request, then immediately end the
        # session. The retry path must refresh revision/cursor/summary from the
        # authoritative session before issuing the end request.
        page.unroute("**/api/v1/practice-sessions/*/attempts")
        expect(page.locator("[data-testid='retry-unknown-btn']")).to_be_visible()
        page.locator("[data-testid='retry-unknown-btn']").click()
        expect(page.locator("[data-testid='feedback-panel']")).to_be_visible()
        page.locator("[data-testid='end-session-btn']").click()
        page.locator("[data-testid='confirm-end-btn']").click()
        expect(page.locator("[data-testid='practice-ended-summary']")).to_be_visible()

        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT id, status FROM session WHERE kind = 'practice' ORDER BY created_at DESC LIMIT 1")
            first_session_id, first_status = cur.fetchone()
            assert first_status == "ended", f"Expected ended after retry, got {first_status}"

        # "再练一轮" must discard the old session_id and create a new session
        # for the same bank, rather than reloading the ended result.
        page.locator("[data-testid='restart-practice-btn']").click()
        expect(page.locator("[data-testid='question-stem-text']")).to_be_visible()
        assert "session_id=" not in page.url, f"Restart must not retain old session URL: {page.url}"

        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT id FROM session WHERE kind = 'practice' AND status = 'open' ORDER BY created_at DESC LIMIT 1")
            second_session_id = cur.fetchone()[0]
            assert second_session_id != first_session_id

        # 5. Exercise the refresh recovery path on the new session too.
        intercepted = False
        page.route("**/api/v1/practice-sessions/*/attempts", handle_attempts_route)
        page.locator("[data-testid='option-item-0']").click()
        page.wait_for_timeout(600)
        assert intercepted
        page.unroute("**/api/v1/practice-sessions/*/attempts")
        page.reload(wait_until="networkidle")

        # 6. Verify page converges to the recovered feedback state (PR-10, PR-15)
        expect(page.locator("[data-testid='practice-view']")).to_be_visible()
        expect(page.locator("[data-testid='feedback-panel']")).to_be_visible()

        # Verify summary count is 1
        expect(page.locator("[data-testid='answer-sheet']")).to_contain_text("已答 1")

        # Verify DB still has exactly 1 attempt (no duplication during recovery)
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT count(*) FROM attempt_event")
            attempt_count_after = cur.fetchone()[0]
            assert attempt_count_after == 2, f"Expected exactly 2 attempts across two sessions, got {attempt_count_after}"

        return {
            "status": "passed",
            "artifacts": harness.artifacts,
            "details": {
                "server_accepted_response_lost_simulated": True,
                "retry_then_end_revision_verified": True,
                "restart_created_new_session": True,
                "reload_converged_feedback": True,
                "idempotent_no_duplicate_attempt": True,
            },
        }
