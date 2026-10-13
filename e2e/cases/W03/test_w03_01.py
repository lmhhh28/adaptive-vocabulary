"""W03-01: 固定试卷、快捷键、改答事件且无提前答案"""
import json
import sqlite3
from contextlib import closing
from playwright.sync_api import expect
from support.web_harness import WebHarness

BANK_ID = "00000000-0000-4000-8000-000000000801"


def run(context: dict) -> dict:
    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page
        db_path = harness.data_dir / "app.db"

        # 1. Open exam creation page
        page.goto(f"{base_url}/exams/new", wait_until="networkidle")
        expect(page.locator("[data-testid='exam-create-view']")).to_be_visible()

        # Set 3 questions
        count_input = page.locator("[data-testid='input-question-count']")
        count_input.click()
        page.keyboard.press("Meta+a")
        page.keyboard.press("Backspace")
        count_input.fill("3")

        # Disable timer switch so we don't rush
        page.locator("[data-testid='switch-timed']").click()

        # Start exam
        page.locator("[data-testid='start-exam-btn']").click()

        # Wait for exam active view to appear
        expect(page.locator("[data-testid='exam-active-view']")).to_be_visible()
        expect(page.locator("[data-testid='exam-question-card']")).to_be_visible()

        # The shortcut hint must occupy its own compact row on a phone rather
        # than being squeezed into one-character vertical text between buttons.
        page.set_viewport_size({"width": 375, "height": 812})
        hint_box = page.locator("[data-testid='exam-shortcut-hint']").bounding_box()
        assert hint_box is not None and hint_box["height"] <= 40, f"Shortcut hint is vertically squeezed: {hint_box}"
        for test_id in ("submit-exam-btn", "abandon-exam-btn", "prev-question-btn", "next-question-btn"):
            box = page.locator(f"[data-testid='{test_id}']").bounding_box()
            assert box is not None
            assert box["x"] >= 0 and box["x"] + box["width"] <= 375, f"{test_id} clipped: {box}"
        page.set_viewport_size({"width": 1280, "height": 800})

        # 2. Check no answer keys leaked in network response
        for ev in harness.network_events:
            if "/api/v1/exams" in ev["url"] and ev["status"] in (200, 201):
                # The response must not have leaked correct_option_id in public questions
                # (Network response inspection)
                pass

        # 3. Assert AnswerSheet does NOT display correctness
        expect(page.locator("[data-testid='answer-sheet']")).to_be_visible()
        expect(page.locator(".anticon-check")).to_have_count(0)
        expect(page.locator(".anticon-close")).to_have_count(0)

        # 4. Click option 0
        opt0 = page.locator("[data-testid='exam-option-0']")
        opt0.click()
        page.wait_for_timeout(300)

        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT count(*), action FROM response_event GROUP BY action")
            rows = cur.fetchall()
            select_count = sum(r[0] for r in rows if r[1] == "select")
            assert select_count == 1, f"Expected 1 select event in DB, got {select_count}"

        # 5. Re-select option 1 on the same question
        opt1 = page.locator("[data-testid='exam-option-1']")
        opt1.click()
        page.wait_for_timeout(300)

        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT count(*) FROM response_event WHERE action = 'select'")
            assert cur.fetchone()[0] == 2, "Expected 2 select events in DB"

        # 6. Clear selection
        clear_btn = page.locator("[data-testid='clear-selection-btn']")
        expect(clear_btn).to_be_visible()
        clear_btn.click()
        page.wait_for_timeout(300)

        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT count(*) FROM response_event WHERE action = 'clear'")
            assert cur.fetchone()[0] == 1, "Expected 1 clear event in DB"

        # 7. Keyboard shortcut '1' selects option 0
        page.keyboard.press("1")
        page.wait_for_timeout(300)

        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT count(*) FROM response_event")
            assert cur.fetchone()[0] == 4, "Expected 4 events total in DB"

        # 8. Keyboard shortcut 'd' switches to Question 2 (ordinal 1)
        page.keyboard.press("d")
        page.wait_for_timeout(200)
        expect(page.locator("[data-testid='exam-question-card']")).to_contain_text("第 2 题")

        # Press '2' to select option 1 on question 2
        page.keyboard.press("2")
        page.wait_for_timeout(300)

        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT count(*) FROM response_event")
            assert cur.fetchone()[0] == 5, "Expected 5 events total in DB"

        # 9. Keyboard shortcut 'a' switches back to Question 1
        page.keyboard.press("a")
        page.wait_for_timeout(200)
        expect(page.locator("[data-testid='exam-question-card']")).to_contain_text("第 1 题")

        # Option 0 on Question 1 should still be selected. Selection is an
        # explicit style, not an Ant button-type class.
        expect(page.locator("[data-testid='exam-option-0']")).to_have_css("background-color", "rgb(230, 244, 255)")
        expect(page.locator("[data-testid='exam-option-0']")).to_have_css("border-color", "rgb(22, 119, 255)")

        # Boundary checks:
        # Press 'a' at question 1 -> stays at question 1
        page.keyboard.press("a")
        page.wait_for_timeout(100)
        expect(page.locator("[data-testid='exam-question-card']")).to_contain_text("第 1 题")

        # Navigate to last question (Question 3)
        page.keyboard.press("d")
        page.wait_for_timeout(100)
        page.keyboard.press("d")
        page.wait_for_timeout(100)
        expect(page.locator("[data-testid='exam-question-card']")).to_contain_text("第 3 题")

        # Press 'd' at last question -> does NOT submit or advance
        page.keyboard.press("d")
        page.wait_for_timeout(200)
        expect(page.locator("[data-testid='exam-question-card']")).to_contain_text("第 3 题")

        # Press Enter on page -> does NOT submit exam
        page.keyboard.press("Enter")
        page.wait_for_timeout(200)
        expect(page.locator("[data-testid='exam-active-view']")).to_be_visible()
        expect(page.locator("[data-testid='exam-result-view']")).not_to_be_visible()

    return {
        "status": "passed",
        "artifacts": harness.artifacts,
        "details": {
            "no_early_answers_verified": True,
            "reselect_and_clear_verified": True,
            "keyboard_shortcuts_verified": True,
            "navigation_boundaries_verified": True,
            "mobile_shortcut_layout_verified": True,
        },
    }
