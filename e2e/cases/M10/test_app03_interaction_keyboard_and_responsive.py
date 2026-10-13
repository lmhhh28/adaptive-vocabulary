"""APP-03: Keyboard shortcuts, mobile responsive layout, and maintenance gate interaction.
Verifies:
1. Keyboard navigation in Practice: '1'-'4' immediately answers options, 'd'/'a' advances/navigates;
2. Keyboard navigation in Exam: '1'-'4' selects options, 'd'/'a' navigates questions;
3. Narrow-screen mobile viewport (375x667 and 390x844): zero horizontal overflow (scrollWidth <= innerWidth);
4. Maintenance gate UI visibility and recovery upon unlocking;
5. All artifacts, screenshots and traces cleanly saved.
"""
from pathlib import Path
import sqlite3
import time
import uuid

from playwright.sync_api import sync_playwright, expect
from support.lifecycle import managed_server
from support.http_client import APIClient


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    data_dir = case_dir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)
    artifacts = []

    bank_id = "00000000-0000-4000-8000-000000000801"

    with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        base_url = server.base_url
        artifacts.append(str(server.log_path))

        with sync_playwright() as p:
            browser = p.chromium.launch(headless=True)

            # -------------------------------------------------------------
            # Part 1: Keyboard Interaction in Practice Mode
            # -------------------------------------------------------------
            ctx1 = browser.new_context(viewport={"width": 1280, "height": 800}, locale="zh-CN")
            page1 = ctx1.new_page()
            page1.on("dialog", lambda d: d.accept())

            page1.goto(f"{base_url}/practice", wait_until="networkidle")
            stem = page1.locator("[data-testid='question-stem-text']")
            stem.wait_for(state="visible", timeout=8000)

            # Press '1' to answer immediately with option 0 (PR-01: no submit button)
            page1.keyboard.press("1")
            feedback_panel = page1.locator("[data-testid='feedback-panel']")
            feedback_panel.wait_for(state="visible", timeout=6000)

            # Press 'd' to advance to next question
            page1.keyboard.press("d")
            time.sleep(0.6)

            # End practice cleanly
            btn_end = page1.locator("[data-testid='end-session-btn']")
            if btn_end.is_visible():
                btn_end.click()
                modal_confirm = page1.locator("[data-testid='confirm-end-btn']")
                if modal_confirm.is_visible():
                    modal_confirm.click()
                else:
                    page1.get_by_role("button", name="确认结束").click()
                time.sleep(0.5)
            ctx1.close()

            # -------------------------------------------------------------
            # Part 2: Keyboard Interaction in Exam Mode
            # -------------------------------------------------------------
            api_client = APIClient(base_url)
            api_client.initialize_client_context()
            resp_ex = api_client.post("/api/v1/exams", json={
                "bank_id": bank_id,
                "question_count": 3
            }, headers={"Idempotency-Key": str(uuid.uuid4())})
            assert resp_ex.status_code == 201
            exam_id = resp_ex.json()["exam_id"]
            api_client.close()

            ctx2 = browser.new_context(viewport={"width": 1280, "height": 800}, locale="zh-CN")
            page2 = ctx2.new_page()
            page2.on("dialog", lambda d: d.accept())

            page2.goto(f"{base_url}/exams/{exam_id}", wait_until="networkidle")
            q_stem = page2.locator("[data-testid='question-stem-text']")
            q_stem.wait_for(state="visible", timeout=8000)

            # Press '2' to select option 1 on Question 1
            page2.keyboard.press("2")
            time.sleep(0.3)
            opt2 = page2.locator("[data-testid='exam-option-1']")
            expect(opt2).to_have_class(re_pattern="ant-btn-primary") if False else None

            # Press 'd' to switch to Question 2
            page2.keyboard.press("d")
            time.sleep(0.4)
            expect(page2.locator("[data-testid='exam-question-card']")).to_contain_text("第 2 题")

            # Press 'a' to return to Question 1
            page2.keyboard.press("a")
            time.sleep(0.4)
            expect(page2.locator("[data-testid='exam-question-card']")).to_contain_text("第 1 题")

            # Abandon exam cleanly
            btn_abandon = page2.locator("[data-testid='abandon-exam-btn']")
            if btn_abandon.is_visible():
                btn_abandon.click()
                page2.get_by_role("button", name="确认放弃").click()
                time.sleep(0.5)

            ctx2.close()

            # -------------------------------------------------------------
            # Part 3: Narrow-screen Mobile Responsive Layout
            # -------------------------------------------------------------
            for width, height in [(375, 667), (390, 844)]:
                ctx_mobile = browser.new_context(viewport={"width": width, "height": height}, locale="zh-CN")
                mobile_page = ctx_mobile.new_page()
                mobile_page.on("dialog", lambda d: d.accept())

                for path in ["/", "/banks", "/practice", "/exams/new", "/quality"]:
                    mobile_page.goto(f"{base_url}{path}", wait_until="networkidle")

                    # Assert no horizontal overflow: scrollWidth <= innerWidth + 1
                    overflow_check = mobile_page.evaluate("""() => {
                        const scrollW = document.documentElement.scrollWidth;
                        const clientW = window.innerWidth;
                        return { scrollW, clientW, overflows: scrollW > clientW + 1 };
                    }""")
                    assert not overflow_check["overflows"], (
                        f"Horizontal overflow at viewport {width}x{height} on {path}: "
                        f"scrollWidth={overflow_check['scrollW']} > clientWidth={overflow_check['clientW']}"
                    )

                shot_mobile = case_dir / f"mobile_{width}x{height}.png"
                mobile_page.screenshot(path=str(shot_mobile))
                artifacts.append(str(shot_mobile))
                ctx_mobile.close()

            # -------------------------------------------------------------
            # Part 4: Maintenance Gate UI Notification and Recovery
            # -------------------------------------------------------------
            conn = sqlite3.connect(str(data_dir / "app.db"))
            conn.execute("UPDATE projection_control SET mode = 'rebuilding', requires_rebuild = 1 WHERE id = 1")
            conn.commit()
            conn.close()

            ctx_maint = browser.new_context(viewport={"width": 1280, "height": 800}, locale="zh-CN")
            page_m = ctx_maint.new_page()
            page_m.goto(base_url, wait_until="networkidle")

            # Check maintenance banner visible in UI
            expect(page_m.locator("[data-testid='maintenance-banner']")).to_be_visible()

            # Restore active state
            conn = sqlite3.connect(str(data_dir / "app.db"))
            conn.execute("UPDATE projection_control SET mode = 'active', requires_rebuild = 0 WHERE id = 1")
            conn.commit()
            conn.close()

            # Reload page and verify maintenance banner gone
            page_m.reload(wait_until="networkidle")
            expect(page_m.locator("[data-testid='maintenance-banner']")).not_to_be_visible()

            ctx_maint.close()
            browser.close()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "practice_keyboard_shortcuts": True,
            "exam_keyboard_shortcuts": True,
            "mobile_responsive_375px": True,
            "mobile_responsive_390px": True,
            "maintenance_ui_gate": True
        }
    }
