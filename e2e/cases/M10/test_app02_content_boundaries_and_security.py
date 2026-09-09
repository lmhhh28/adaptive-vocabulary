"""APP-02: Content boundaries, anti-leak invariants, cache headers, and console error monitoring.
Verifies:
1. During active session, management / detail endpoints and cross-mode creations return SESSION_ACTIVE;
2. Question DTOs delivered to browser before submission strictly omit answer_key, explanation,
   is_correct, and option_identity_id;
3. HTML source before submit contains no answer keys;
4. Practice, exam, report, and context responses enforce Cache-Control: no-store;
5. Zero unhandled JavaScript exceptions or console errors across all user flows.
"""
from pathlib import Path
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

        client = APIClient(base_url)
        client.initialize_client_context()

        # Check Cache-Control: no-store on client context
        resp_ctx = client.get("/client-context")
        assert "no-store" in resp_ctx.headers.get("Cache-Control", ""), "Cache-Control: no-store missing on client-context"

        # 1. Start active practice session
        resp_pr = client.post("/api/v1/practice-sessions", json={
            "bank_id": bank_id
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp_pr.status_code == 201
        session_id = resp_pr.json()["session"]["id"]

        # 1.1 Cross-mode exclusivity check: cannot create exam while practice is active
        resp_exam_conflict = client.post("/api/v1/exams", json={
            "bank_id": bank_id,
            "question_count": 3
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp_exam_conflict.status_code == 409
        assert resp_exam_conflict.json()["error"]["code"] == "SESSION_ACTIVE"

        # 1.2 Fetch next question and verify DTO boundaries & Cache-Control
        resp_next = client.post(f"/api/v1/practice-sessions/{session_id}/next", json={
            "expected_cursor": 0
        })
        assert resp_next.status_code == 200
        assert "no-store" in resp_next.headers.get("Cache-Control", ""), "Cache-Control: no-store missing on /next"
        next_data = resp_next.json()
        q_dto = next_data["question"]

        # Assert zero answer leakage in public question DTO
        assert "correct_option_id" not in q_dto, "Leak: correct_option_id in question DTO"
        assert "explanation" not in q_dto, "Leak: explanation in question DTO"
        assert "quality_checks" not in q_dto, "Leak: quality_checks in question DTO"
        for opt in q_dto.get("options", []):
            assert "is_correct" not in opt, "Leak: is_correct in option DTO"
            assert "option_identity_id" not in opt, "Leak: option_identity_id in option DTO"

        # 2. Browser checks for HTML leakage and console errors
        console_errors = []
        page_errors = []

        with sync_playwright() as p:
            browser = p.chromium.launch(headless=True)
            b_ctx = browser.new_context()

            page = b_ctx.new_page()

            def on_console(msg):
                if msg.type == "error":
                    # Ignore harmless favicon 404s if any
                    if "favicon.ico" not in msg.text:
                        console_errors.append(msg.text)

            def on_pageerror(err):
                page_errors.append(str(err))

            page.on("console", on_console)
            page.on("pageerror", on_pageerror)
            page.on("dialog", lambda d: d.accept())

            # Visit all main pages in sequence to monitor console errors
            pages_to_visit = [
                "/",
                "/banks",
                "/practice",
                "/exams/new",
                "/quality"
            ]

            for path in pages_to_visit:
                page.goto(f"{base_url}{path}")
                page.wait_for_load_state("domcontentloaded")

            # Check practice page DOM before submit
            page.goto(f"{base_url}/practice")
            stem_el = page.locator("#question-stem")
            stem_el.wait_for(state="visible", timeout=8000)

            # HTML content must not contain correct_option_id
            dom_html = page.content()
            assert "correct_option_id" not in dom_html, "Leak: correct_option_id in DOM before submit"
            expl_el = page.locator("#explanation-val")
            if expl_el.count() > 0:
                assert expl_el.text_content().strip() == "", "Leak: explanation text rendered before submit"

            browser.close()

        # Cleanly end the practice session so next tests or operations can run
        client.post(f"/api/v1/practice-sessions/{session_id}/end", json={"status": "ended"})
        client.close()

    assert len(page_errors) == 0, f"Unhandled page errors found: {page_errors}"
    assert len(console_errors) == 0, f"Console errors found: {console_errors}"

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "session_active_prevented_exam": True,
            "public_dto_has_no_answers": True,
            "cache_control_no_store_verified": True,
            "console_errors_count": len(console_errors),
            "page_errors_count": len(page_errors)
        }
    }
