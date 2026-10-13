"""W02-03: 错误持续停留及主动下一题"""
import sqlite3
from contextlib import closing
from playwright.sync_api import expect
from support.web_harness import WebHarness


def get_options_info(db_path, session_id, ordinal):
    with closing(sqlite3.connect(db_path)) as conn:
        cur = conn.cursor()
        cur.execute(
            """
            SELECT qv.correct_option_id
            FROM question_instance qi
            JOIN question_version qv ON qi.question_version_id = qv.id
            WHERE qi.session_id = ? AND qi.ordinal = ?
            """,
            (session_id, ordinal),
        )
        row = cur.fetchone()
        if not row:
            return None
        return row[0]


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

        session_id = get_active_session_id(db_path)
        assert session_id, "Expected active session in DB"

        # 2. Pick an incorrect option
        correct_opt = get_options_info(db_path, session_id, 0)
        assert correct_opt, "Expected correct option ID"

        # Find any option whose data-option-id is NOT correct_opt
        option_elements = page.locator("[data-testid='practice-options'] button")
        count = option_elements.count()
        assert count == 4, f"Expected 4 options, got {count}"

        wrong_opt_btn = None
        for i in range(count):
            opt_btn = option_elements.nth(i)
            opt_id = opt_btn.get_attribute("data-option-id")
            if opt_id != correct_opt:
                wrong_opt_btn = opt_btn
                break

        assert wrong_opt_btn is not None, "Could not find an incorrect option"
        wrong_opt_btn.click()

        # 3. Verify incorrect feedback is displayed (PR-06)
        feedback_panel = page.locator("[data-testid='feedback-panel']")
        expect(feedback_panel).to_be_visible()
        expect(feedback_panel).to_contain_text("回答错误")

        # 4. Wait for 1.2s to confirm no auto-advance timer exists for errors
        page.wait_for_timeout(1200)
        expect(page.locator("[data-testid='question-stem-container']")).to_contain_text("第 1 题")
        expect(feedback_panel).to_be_visible()
        expect(feedback_panel).to_contain_text("回答错误")

        # 5. Refresh page and verify error feedback restoration (PR-07)
        page.reload(wait_until="networkidle")
        expect(page.locator("[data-testid='question-stem-container']")).to_contain_text("第 1 题")
        expect(page.locator("[data-testid='feedback-panel']")).to_be_visible()
        expect(page.locator("[data-testid='feedback-panel']")).to_contain_text("回答错误")

        # Wait another 800ms to ensure restored feedback does not auto-advance either (PR-08)
        page.wait_for_timeout(800)
        expect(page.locator("[data-testid='question-stem-container']")).to_contain_text("第 1 题")

        # 6. Manually advance via keyboard 'D' (PR-09)
        page.keyboard.press("d")
        page.wait_for_timeout(500)
        expect(page.locator("[data-testid='question-stem-container']")).to_contain_text("第 2 题")
        expect(page.locator("[data-testid='feedback-panel']")).not_to_be_visible()

        return {
            "status": "passed",
            "artifacts": harness.artifacts,
            "details": {
                "incorrect_stays_indefinitely": True,
                "refresh_restores_error_feedback": True,
                "restored_feedback_never_advances": True,
                "manual_next_succeeds": True,
            },
        }
