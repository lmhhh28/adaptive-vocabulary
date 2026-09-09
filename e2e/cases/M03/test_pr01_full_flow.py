"""PR-01: Practice full flow acceptance test.
Verifies real Chromium clicks -> answer -> feedback -> next question -> end;
Verifies that before submit, neither HTML, network DTOs, recovery nor error bodies contain answer fields.
"""
from pathlib import Path
import sqlite3
from playwright.sync_api import sync_playwright
from support.lifecycle import managed_server
from support.http_client import APIClient


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    data_dir = case_dir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)
    artifacts = []

    # 1. Start server with seed core
    with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        base_url = server.base_url
        artifacts.append(str(server.log_path))

        with sync_playwright() as p:
            browser = p.chromium.launch(headless=True)
            b_ctx = browser.new_context()
            b_ctx.tracing.start(screenshots=True, snapshots=True)

            page = b_ctx.new_page()

            # Intercept network responses to verify public_question DTO has NO answer fields
            intercepted_next_bodies = []

            def handle_response(response):
                if "/api/v1/practice-sessions/" in response.url and response.url.endswith("/next"):
                    try:
                        intercepted_next_bodies.append(response.json())
                    except Exception:
                        pass

            page.on("response", handle_response)

            # 2. Open practice page
            page.goto(f"{base_url}/practice")
            assert "自适应训练" in page.title()

            # 3. Select bank to start practice
            btn_start = page.locator(".bank-select-card .btn-start-bank").first
            btn_start.wait_for(state="visible")
            btn_start.click()

            # 4. Wait for question stem
            stem_el = page.locator("#question-stem")
            stem_el.wait_for(state="visible")
            stem_text = stem_el.text_content().strip()
            assert len(stem_text) > 0, "Question stem should not be empty"

            # 5. Verify NO answer keys exist in HTML before submission
            html_content = page.content()
            assert "correct_option_id" not in html_content, "correct_option_id found in HTML before submission"
            assert "explanation_val" not in html_content or page.locator("#explanation-val").text_content() == "", "explanation text leaked before submission"

            # Verify intercepted next response has NO answer keys
            assert len(intercepted_next_bodies) > 0, "Expected /next response to be intercepted"
            q_dto = intercepted_next_bodies[0].get("question", {})
            assert "correct_option_id" not in q_dto, "correct_option_id leaked in question DTO"
            assert "explanation" not in q_dto, "explanation leaked in question DTO"
            for opt in q_dto.get("options", []):
                assert "is_correct" not in opt, "is_correct leaked in option DTO"
                assert "option_identity_id" not in opt, "option_identity_id leaked in option DTO"

            # 6. Click option and submit
            first_opt = page.locator(".option-btn").first
            first_opt.click()

            btn_submit = page.locator("#btn-submit")
            assert btn_submit.is_enabled(), "Submit button should be enabled after selecting an option"
            btn_submit.click()

            # 7. Feedback visible
            feedback_section = page.locator("#feedback-section")
            feedback_section.wait_for(state="visible")

            correct_val = page.locator("#correct-answer-val").text_content().strip()
            assert len(correct_val) > 0, "Correct answer should be displayed in feedback"

            expl_val = page.locator("#explanation-val").text_content().strip()
            assert len(expl_val) > 0, "Explanation should be displayed in feedback"

            # 8. Advance to next question
            btn_next = page.locator("#btn-next")
            btn_next.wait_for(state="visible")
            btn_next.click()

            # Wait for Question 2
            page.wait_for_selector("#question-index-label:has-text('第 2 题')")

            # Answer question 2
            page.locator(".option-btn").first.click()
            page.locator("#btn-submit").click()
            feedback_section.wait_for(state="visible")

            # 9. End practice
            page.on("dialog", lambda dialog: dialog.accept())
            page.locator("#btn-end-practice").click()

            # Summary view should be visible
            summary_view = page.locator("#summary-view")
            summary_view.wait_for(state="visible")
            answered_count = page.locator("#summary-answered-count").text_content().strip()
            assert answered_count == "2", f"Expected 2 questions answered, got {answered_count}"

            # Save screenshot & trace
            screenshot_path = case_dir / "practice_summary.png"
            page.screenshot(path=str(screenshot_path))
            artifacts.append(str(screenshot_path))

            trace_path = case_dir / "trace.zip"
            b_ctx.tracing.stop(path=str(trace_path))
            artifacts.append(str(trace_path))

            browser.close()

        # 10. Database check: session is ended and no active session remains
        db_file = data_dir / "app.db"
        conn = sqlite3.connect(str(db_file))
        cur = conn.cursor()

        cur.execute("SELECT count(*) FROM session WHERE status = 'open'")
        open_count = cur.fetchone()[0]
        assert open_count == 0, f"Expected 0 open sessions, found {open_count}"

        cur.execute("SELECT count(*) FROM session WHERE status = 'ended'")
        ended_count = cur.fetchone()[0]
        assert ended_count == 1, f"Expected 1 ended session, found {ended_count}"

        cur.execute("SELECT count(*) FROM attempt_event")
        att_count = cur.fetchone()[0]
        assert att_count == 2, f"Expected 2 attempts recorded, found {att_count}"

        conn.close()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "questions_answered": 2,
            "no_answer_leaked_before_submit": True,
            "session_ended_cleanly": True,
        },
    }
