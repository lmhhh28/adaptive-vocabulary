"""W02-01: 点击和数字即答、无提交按钮、防双击"""
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

        # 1. Open practice page with bank_id
        page.goto(f"{base_url}/practice?bank_id={bank_id}", wait_until="networkidle")

        # 2. Verify question stem is rendered
        expect(page.locator("[data-testid='practice-view']")).to_be_visible()
        expect(page.locator("[data-testid='question-stem-text']")).to_be_visible()
        expect(page.locator("[data-testid='practice-options']")).to_be_visible()

        # 3. Assert NO submit button exists anywhere on page
        assert page.get_by_role("button", name="提交").count() == 0, "Practice page must NOT have a submit button"
        assert page.get_by_role("button", name="提交答案").count() == 0, "Practice page must NOT have a submit button"
        assert page.get_by_role("button", name="确认答案").count() == 0, "Practice page must NOT have a confirm button"

        # 4. Test anti-double-click / mutual exclusion lock (PR-01, PR-02)
        opt0 = page.locator("[data-testid='option-item-0']")
        expect(opt0).to_be_visible()
        opt0.click(click_count=2)

        # 5. Wait for feedback to appear
        expect(page.locator("[data-testid='feedback-panel']")).to_be_visible()

        # Check DB that exactly 1 attempt was recorded in attempt_event
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT count(*) FROM attempt_event")
            attempt_count_1 = cur.fetchone()[0]
            assert attempt_count_1 == 1, f"Expected 1 attempt after double click, got {attempt_count_1}"

        # 6. Advance to next question
        if page.locator("[data-testid='next-question-btn']").is_visible():
            page.locator("[data-testid='next-question-btn']").click()
        else:
            page.keyboard.press("d")

        # Wait for next question (ordinal 1)
        page.wait_for_timeout(400)
        expect(page.locator("[data-testid='question-stem-text']")).to_be_visible()
        expect(page.locator("[data-testid='feedback-panel']")).not_to_be_visible()

        # 7. Test numeric keyboard shortcut answering (PR-17)
        page.keyboard.press("2")
        expect(page.locator("[data-testid='feedback-panel']")).to_be_visible()

        # Check DB that now exactly 2 attempts are recorded in attempt_event
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT count(*) FROM attempt_event")
            attempt_count_2 = cur.fetchone()[0]
            assert attempt_count_2 == 2, f"Expected 2 attempts in total, got {attempt_count_2}"

        return {
            "status": "passed",
            "artifacts": harness.artifacts,
            "details": {
                "no_submit_button_verified": True,
                "anti_double_click_verified": True,
                "keyboard_shortcut_verified": True,
                "attempt_count": attempt_count_2,
            },
        }
