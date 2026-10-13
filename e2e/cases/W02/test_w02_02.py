"""W02-02: 正确自动前进与导航/解析/页面隐藏取消"""
import json
import sqlite3
from contextlib import closing
from playwright.sync_api import expect
from support.web_harness import WebHarness


def get_correct_option_id(db_path, session_id, ordinal):
    with closing(sqlite3.connect(db_path)) as conn:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT snapshot_json
            FROM question_instance
            WHERE session_id = ? AND ordinal = ?
            """,
            (session_id, ordinal),
        )
        row = cur.fetchone()
        if not row:
            return None
        return json.loads(row[0]).get("correct_option_id")


def get_active_session_id(db_path):
    with closing(sqlite3.connect(db_path)) as conn:
        cur = conn.cursor()
        cur.execute("SELECT id FROM session WHERE status = 'open' AND kind = 'practice' ORDER BY created_at DESC LIMIT 1")
        row = cur.fetchone()
        return row[0] if row else None


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

        session_id = get_active_session_id(db_path)
        assert session_id, "Expected active practice session in DB"

        # -------------------------------------------------------------
        # 2. Test Auto-advance on Correct Answer (PR-03)
        # -------------------------------------------------------------
        correct_opt_0 = get_correct_option_id(db_path, session_id, 0)
        assert correct_opt_0, "Expected correct_option_id for ordinal 0"

        # Click the correct option
        page.locator(f"[data-option-id='{correct_opt_0}']").click()
        expect(page.locator("[data-testid='feedback-panel']")).to_be_visible()
        expect(page.locator("[data-testid='feedback-panel']")).to_contain_text("回答正确")

        # Wait for auto-advance (500ms timeout). At 800ms it must have advanced to ordinal 1!
        page.wait_for_timeout(800)
        expect(page.locator("[data-testid='question-stem-container']")).to_contain_text("第 2 题")
        expect(page.locator("[data-testid='feedback-panel']")).not_to_be_visible()

        # -------------------------------------------------------------
        # 3. Test Auto-advance Cancelled by "留在本题" (PR-04)
        # -------------------------------------------------------------
        correct_opt_1 = get_correct_option_id(db_path, session_id, 1)
        assert correct_opt_1, "Expected correct_option_id for ordinal 1"

        page.locator(f"[data-option-id='{correct_opt_1}']").click()
        expect(page.locator("[data-testid='feedback-panel']")).to_be_visible()
        expect(page.locator("[data-testid='feedback-panel']")).to_contain_text("回答正确")

        # Immediately click "留在本题" to cancel the 500ms countdown
        stay_btn = page.locator("[data-testid='stay-on-question-btn']")
        expect(stay_btn).to_be_visible()
        stay_btn.click()

        # Wait 800ms to verify that auto-advance did NOT happen
        page.wait_for_timeout(800)
        expect(page.locator("[data-testid='question-stem-container']")).to_contain_text("第 2 题")
        expect(page.locator("[data-testid='feedback-panel']")).to_be_visible()

        # Now manually click Next (D) to advance
        page.locator("[data-testid='bottom-next-btn']").click()
        page.wait_for_timeout(400)
        expect(page.locator("[data-testid='question-stem-container']")).to_contain_text("第 3 题")
        expect(page.locator("[data-testid='feedback-panel']")).not_to_be_visible()

        # -------------------------------------------------------------
        # 4. Test Auto-advance Cancelled by Switch Toggle (PR-05)
        # -------------------------------------------------------------
        correct_opt_2 = get_correct_option_id(db_path, session_id, 2)
        assert correct_opt_2, "Expected correct_option_id for ordinal 2"

        page.locator(f"[data-option-id='{correct_opt_2}']").click()
        expect(page.locator("[data-testid='feedback-panel']")).to_be_visible()
        expect(page.locator("[data-testid='feedback-panel']")).to_contain_text("回答正确")

        # Toggle auto-advance switch OFF immediately
        page.locator("[data-testid='auto-advance-switch']").click()

        # Wait 800ms and verify it remained on Question 3
        page.wait_for_timeout(800)
        expect(page.locator("[data-testid='question-stem-container']")).to_contain_text("第 3 题")
        expect(page.locator("[data-testid='feedback-panel']")).to_be_visible()

        return {
            "status": "passed",
            "artifacts": harness.artifacts,
            "details": {
                "auto_advance_500ms_verified": True,
                "cancel_by_stay_button_verified": True,
                "cancel_by_switch_toggle_verified": True,
            },
        }
