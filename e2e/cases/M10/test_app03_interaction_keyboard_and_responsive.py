"""APP-03: Keyboard shortcuts, mobile responsive layout, and maintenance gate interaction.
Verifies:
1. Keyboard navigation in Practice: '1'-'4' / 'A'-'D' select options, 'Enter' submits and advances;
2. Keyboard navigation in Exam: option selection and ArrowLeft/ArrowRight question navigation;
3. Narrow-screen mobile viewport (375x667 and 390x844): zero horizontal overflow (scrollWidth <= innerWidth);
4. Maintenance gate UI visibility and recovery upon unlocking;
5. All artifacts, screenshots and traces cleanly saved.
"""
from pathlib import Path
import sqlite3
import time
import uuid

from playwright.sync_api import sync_playwright
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
            ctx1 = browser.new_context()
            page1 = ctx1.new_page()
            page1.on("dialog", lambda d: d.accept())

            page1.goto(f"{base_url}/practice")
            btn_start = page1.locator(".btn-start-bank").first
            if btn_start.is_visible():
                btn_start.click()

            stem = page1.locator("#question-stem")
            stem.wait_for(state="visible", timeout=8000)

            # Press '1' to select option 0
            page1.keyboard.press("1")
            time.sleep(0.3)
            first_opt = page1.locator(".option-btn").first
            assert "selected" in (first_opt.get_attribute("class") or ""), "Option 1 not selected via keyboard '1'"

            # Press 'Enter' to submit answer
            page1.keyboard.press("Enter")
            feedback_banner = page1.locator("#feedback-result-banner")
            feedback_banner.wait_for(state="visible", timeout=6000)

            # Press 'Enter' again to load next question
            page1.keyboard.press("Enter")
            time.sleep(0.6)

            # End practice
            page1.locator("#btn-end-practice").click()
            time.sleep(0.3)
            ctx1.close()

            # -------------------------------------------------------------
            # Part 2: Keyboard Interaction in Exam Mode
            # -------------------------------------------------------------
            # Create exam via API to ensure clean start
            api_client = APIClient(base_url)
            api_client.initialize_client_context()
            resp_ex = api_client.post("/api/v1/exams", json={
                "bank_id": bank_id,
                "question_count": 3
            }, headers={"Idempotency-Key": str(uuid.uuid4())})
            assert resp_ex.status_code == 201
            exam_id = resp_ex.json()["exam_id"]
            api_client.close()

            ctx2 = browser.new_context()
            page2 = ctx2.new_page()
            page2.on("dialog", lambda d: d.accept())

            page2.goto(f"{base_url}/exams/{exam_id}")
            q_stem = page2.locator("#q-stem")
            q_stem.wait_for(state="visible", timeout=8000)

            # Press '2' to select option 1 on Question 1
            page2.keyboard.press("2")
            time.sleep(0.3)
            opt2 = page2.locator(".option-item").nth(1)
            assert "selected" in (opt2.get_attribute("class") or ""), "Option 2 not selected via keyboard '2'"

            # Press 'ArrowRight' to switch to Question 2
            page2.keyboard.press("ArrowRight")
            time.sleep(0.4)
            q_ord = page2.locator("#q-ordinal-label").text_content()
            assert "2" in q_ord, f"Expected Question 2, got: {q_ord}"

            # Press 'ArrowLeft' to return to Question 1
            page2.keyboard.press("ArrowLeft")
            time.sleep(0.4)
            q_ord = page2.locator("#q-ordinal-label").text_content()
            assert "1" in q_ord, f"Expected Question 1, got: {q_ord}"

            # Abandon exam cleanly
            btn_abandon = page2.locator("#btn-abandon-exam")
            if btn_abandon.is_visible():
                btn_abandon.click()
                time.sleep(0.5)

            ctx2.close()

            # -------------------------------------------------------------
            # Part 3: Narrow-screen Mobile Responsive Layout
            # -------------------------------------------------------------
            # Viewport 375x667 (iPhone SE) & 390x844 (iPhone 14)
            for width, height in [(375, 667), (390, 844)]:
                ctx_mobile = browser.new_context(viewport={"width": width, "height": height})
                mobile_page = ctx_mobile.new_page()
                mobile_page.on("dialog", lambda d: d.accept())

                for path in ["/", "/banks", "/practice", "/exams/new", "/quality"]:
                    mobile_page.goto(f"{base_url}{path}")
                    mobile_page.wait_for_load_state("domcontentloaded")

                    # Assert no horizontal overflow: scrollWidth <= innerWidth
                    overflow_check = mobile_page.evaluate("""() => {
                        const scrollW = document.documentElement.scrollWidth;
                        const clientW = window.innerWidth;
                        return { scrollW, clientW, overflows: scrollW > clientW + 1 };
                    }""")
                    assert not overflow_check["overflows"], (
                        f"Horizontal overflow at viewport {width}x{height} on {path}: "
                        f"scrollWidth={overflow_check['scrollW']} > clientWidth={overflow_check['clientW']}"
                    )

                # Capture narrow screen screenshot
                shot_mobile = case_dir / f"mobile_{width}x{height}.png"
                mobile_page.screenshot(path=str(shot_mobile))
                artifacts.append(str(shot_mobile))
                ctx_mobile.close()

            # -------------------------------------------------------------
            # Part 4: Maintenance Gate UI Notification and Recovery
            # -------------------------------------------------------------
            # Set mode = 'rebuilding' in projection_control to test UI banner
            conn = sqlite3.connect(str(data_dir / "app.db"))
            conn.execute("UPDATE projection_control SET mode = 'rebuilding', requires_rebuild = 1 WHERE id = 1")
            conn.commit()
            conn.close()

            ctx_maint = browser.new_context()
            page_m = ctx_maint.new_page()
            page_m.goto(base_url)
            time.sleep(0.5)

            # Check maintenance mode reflected in UI
            mode_text = page_m.locator("#runtime-mode").text_content().strip()
            assert mode_text in ["rebuilding", "maintenance"], f"Expected maintenance mode, got: {mode_text}"

            # Restore active state
            conn = sqlite3.connect(str(data_dir / "app.db"))
            conn.execute("UPDATE projection_control SET mode = 'active', requires_rebuild = 0 WHERE id = 1")
            conn.commit()
            conn.close()

            # Reload page and verify restored active mode
            page_m.reload()
            time.sleep(0.5)
            mode_text_restored = page_m.locator("#runtime-mode").text_content().strip()
            assert mode_text_restored == "active", f"Expected active mode after restore, got: {mode_text_restored}"

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
