"""W03-04: 待确认队列不能越过交卷屏障"""
import json
import sqlite3
import uuid
from contextlib import closing
from playwright.sync_api import expect
from support.web_harness import WebHarness
from support.http_client import APIClient

BANK_ID = "00000000-0000-4000-8000-000000000801"


def run(context: dict) -> dict:
    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page

        # 1. Create an exam via API
        client = APIClient(base_url)
        client.initialize_client_context()
        create_resp = client.post("/api/v1/exams", json={
            "bank_id": BANK_ID,
            "question_count": 3
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert create_resp.status_code == 201
        exam_id = create_resp.json()["exam_id"]

        # 2. Open exam in browser
        page.goto(f"{base_url}/exams/{exam_id}", wait_until="networkidle")
        expect(page.locator("[data-testid='exam-active-view']")).to_be_visible()

        # 3. Intercept response events route to hold and abort response
        def handle_hold_route(route, request):
            # Abort to force unknown state
            route.abort("failed")

        page.route("**/api/v1/exams/*/response-events", handle_hold_route)

        # 4. Click option 0
        page.locator("[data-testid='exam-option-0']").click()
        page.wait_for_timeout(600)

        # 5. Try clicking "交卷" while in unconfirmed/unknown state
        page.locator("[data-testid='submit-exam-btn']").click()
        page.wait_for_timeout(300)

        # The confirmation modal MUST NOT appear!
        expect(page.locator("[data-testid='submit-confirm-modal']")).not_to_be_visible()
        expect(page.locator("[data-testid='exam-sync-warning']")).to_be_visible()

        # 6. Unroute and recover sync
        page.unroute("**/api/v1/exams/*/response-events")
        page.locator("[data-testid='retry-sync-btn']").click()
        page.wait_for_timeout(600)

        # Sync should be ok now
        expect(page.locator("[data-testid='sync-ok-tag']")).to_be_visible()

        # 7. Now click "交卷"
        page.locator("[data-testid='submit-exam-btn']").click()
        expect(page.locator("[data-testid='submit-confirm-modal']")).to_be_visible()
        expect(page.locator("[data-testid='submit-confirm-modal']")).to_contain_text("已答 1 题")
        expect(page.locator("[data-testid='submit-confirm-modal']")).to_contain_text("未答 2 题")

        # 8. Click "返回检查"
        page.get_by_role("button", name="返回检查").click()
        expect(page.locator("[data-testid='submit-confirm-modal']")).not_to_be_visible()

        # 9. Answer Question 2
        page.locator("[data-testid='next-question-btn']").click()
        page.wait_for_timeout(200)
        page.locator("[data-testid='exam-option-1']").click()
        page.wait_for_timeout(400)

        # 10. Click "交卷" again
        page.locator("[data-testid='submit-exam-btn']").click()
        expect(page.locator("[data-testid='submit-confirm-modal']")).to_be_visible()
        expect(page.locator("[data-testid='submit-confirm-modal']")).to_contain_text("已答 2 题")
        expect(page.locator("[data-testid='submit-confirm-modal']")).to_contain_text("未答 1 题")

        # Confirm submit
        page.get_by_role("button", name="确认交卷").click()
        expect(page.locator("[data-testid='exam-result-view']")).to_be_visible()

        client.close()

    return {
        "status": "passed",
        "artifacts": harness.artifacts,
        "details": {
            "unconfirmed_queue_blocks_submit_modal": True,
            "sync_barrier_lifted_after_resolution": True,
            "submit_modal_counts_verified": True,
        },
    }
