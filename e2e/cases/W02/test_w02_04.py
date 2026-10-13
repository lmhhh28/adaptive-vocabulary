"""W02-04: 超过30题回看、返回当前题、展示记录和计数"""
import sqlite3
from contextlib import closing
from playwright.sync_api import expect
from support.http_client import APIClient
from support.web_harness import WebHarness


def run(context: dict) -> dict:
    bank_id = "00000000-0000-4000-8000-000000000801"
    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page
        db_path = harness.data_dir / "app.db"

        # 1. Prepare 35 answered questions via API client
        client = APIClient(base_url)
        client.initialize_client_context()

        create_resp = client.post("/api/v1/practice-sessions", json={"bank_id": bank_id})
        assert create_resp.status_code == 201, f"Failed create session: {create_resp.text}"
        session_id = create_resp.json()["session"]["id"]

        for ord_idx in range(35):
            nxt = client.post(f"/api/v1/practice-sessions/{session_id}/next", json={"expected_cursor": ord_idx})
            assert nxt.status_code == 200, f"Failed next at {ord_idx}: {nxt.text}"
            q = nxt.json()["question"]
            att = client.post(
                f"/api/v1/practice-sessions/{session_id}/attempts",
                json={"instance_id": q["instance_id"], "option_id": q["options"][0]["id"]},
                headers={"Idempotency-Key": f"key-setup-{ord_idx}"},
            )
            assert att.status_code == 200

        # Now get question 35 as the active unanswered question
        nxt35 = client.post(f"/api/v1/practice-sessions/{session_id}/next", json={"expected_cursor": 35})
        assert nxt35.status_code == 200
        q35 = nxt35.json()["question"]
        assert q35["ordinal"] == 35

        # 2. Open browser page directly to this session
        page.goto(f"{base_url}/practice?session_id={session_id}", wait_until="networkidle")
        expect(page.locator("[data-testid='practice-view']")).to_be_visible()

        # Verify summary counts are consistent with 35 answered items
        expect(page.locator("[data-testid='answer-sheet']")).to_contain_text("已答 35")
        expect(page.locator("[data-testid='question-stem-container']")).to_contain_text("第 36 题")

        # 3. Answer sheet pagination & historical review (PR-18, PR-19)
        # Recovery must load the older page too; the first five questions must
        # not disappear merely because recent_records is capped at 30.
        expect(page.locator("[data-testid='sheet-item-0']")).to_be_visible()

        # Find an answered sheet item (e.g. sheet-item-20)
        sheet_item_20 = page.locator("[data-testid='sheet-item-20']")
        expect(sheet_item_20).to_be_visible()
        sheet_item_20.click()

        # Verify review mode
        expect(page.locator("[data-testid='reviewing-alert']")).to_be_visible()
        expect(page.locator("[data-testid='question-stem-container']")).to_contain_text("第 21 题")

        # Verify options are disabled in review mode
        opt0 = page.locator("[data-testid='option-item-0']")
        expect(opt0).to_be_disabled()

        # Press keys 1, 2, 3, 4 - should not produce any attempts
        page.keyboard.press("1")
        page.keyboard.press("2")
        page.wait_for_timeout(300)

        # Check DB attempt count in attempt_event remains 35
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT count(*) FROM attempt_event WHERE session_id = ?", (session_id,))
            attempts_count = cur.fetchone()[0]
            assert attempts_count == 35, f"Expected 35 attempts, got {attempts_count}"

        # 4. Return to current question (PR-22)
        return_btn = page.locator("[data-testid='return-current-btn']")
        expect(return_btn).to_be_visible()
        return_btn.click()

        # Verify returned to active unanswered question (第 36 题)
        expect(page.locator("[data-testid='reviewing-alert']")).not_to_be_visible()
        expect(page.locator("[data-testid='question-stem-container']")).to_contain_text("第 36 题")
        expect(page.locator("[data-testid='option-item-0']")).to_be_enabled()

        # 5. Out-of-order history responses must not override the user's most
        # recent navigation intent.
        held_routes = []

        def hold_older_history(route):
            held_routes.append(route)

        page.route(
            f"**/api/v1/practice-sessions/{session_id}/answered-questions/18",
            hold_older_history,
        )
        page.locator("[data-testid='sheet-item-18']").click()
        page.wait_for_timeout(100)
        assert held_routes, "Expected ordinal 18 history request to be held"
        page.locator("[data-testid='sheet-item-19']").click()
        expect(page.locator("[data-testid='question-stem-container']")).to_contain_text("第 20 题")

        held_routes[0].fulfill(response=held_routes[0].fetch())
        page.wait_for_timeout(200)
        expect(page.locator("[data-testid='question-stem-container']")).to_contain_text("第 20 题")
        page.unroute(f"**/api/v1/practice-sessions/{session_id}/answered-questions/18")

        # 6. Check audit: practice_feedback_view recorded the historical review
        page.wait_for_timeout(400)
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute(
                "SELECT count(*) FROM practice_feedback_view WHERE session_id = ? AND presentation = 'history'",
                (session_id,),
            )
            audit_count = cur.fetchone()[0]
            assert audit_count >= 1, f"Expected feedback_view audit record, got {audit_count}"

        return {
            "status": "passed",
            "artifacts": harness.artifacts,
            "details": {
                "total_answered_count": 35,
                "older_records_loaded": True,
                "history_response_race_guard_verified": True,
                "review_mode_verified": True,
                "keys_ignored_in_review": True,
                "return_to_current_verified": True,
                "audit_recorded": audit_count,
            },
        }
