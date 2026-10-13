"""W03-03: 队列丢失后的 telemetry-gap"""
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
        db_path = harness.data_dir / "app.db"

        # 1. Create an exam via API
        client = APIClient(base_url)
        client.initialize_client_context()
        create_resp = client.post("/api/v1/exams", json={
            "bank_id": BANK_ID,
            "question_count": 3
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert create_resp.status_code == 201
        exam_id = create_resp.json()["exam_id"]

        # 2. Open exam in browser and answer question 1
        page.goto(f"{base_url}/exams/{exam_id}", wait_until="networkidle")
        expect(page.locator("[data-testid='exam-active-view']")).to_be_visible()

        page.locator("[data-testid='exam-option-0']").click()
        page.wait_for_timeout(400)

        # 3. Simulate queue loss / telemetry gap on backend
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT revision FROM session WHERE id = ?", (exam_id,))
            current_rev = cur.fetchone()[0]

        gap_resp = client.post(f"/api/v1/exams/{exam_id}/telemetry-gaps", json={
            "expected_revision": current_rev,
            "reason": "lost_queue"
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert gap_resp.status_code == 200, f"Gap barrier failed: {gap_resp.text}"
        assert gap_resp.json()["telemetry_incomplete"] is True
        new_rev = gap_resp.json()["current_revision"]
        assert new_rev == current_rev + 1

        # Check DB: telemetry_incomplete == 1
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT telemetry_incomplete FROM exam_session WHERE session_id = ?", (exam_id,))
            assert cur.fetchone()[0] == 1, "Expected telemetry_incomplete=1 in DB"

        # 4. Answer remaining questions in browser (auto-rebase handles revision advance)
        page.locator("[data-testid='next-question-btn']").click()
        page.wait_for_timeout(200)
        page.locator("[data-testid='exam-option-1']").click()
        page.wait_for_timeout(400)

        page.locator("[data-testid='next-question-btn']").click()
        page.wait_for_timeout(200)
        page.locator("[data-testid='exam-option-0']").click()
        page.wait_for_timeout(400)

        # 5. Submit exam
        page.locator("[data-testid='submit-exam-btn']").click()
        expect(page.locator("[data-testid='submit-confirm-modal']")).to_be_visible()
        page.get_by_role("button", name="确认交卷").click()

        # 6. Wait for Result view
        expect(page.locator("[data-testid='exam-result-view']")).to_be_visible()
        expect(page.locator("[data-testid='ratio-stat']")).to_be_visible()

        # 7. Verify telemetry incomplete / no reliable first choice alert is rendered
        expect(page.locator("[data-testid='no-reliable-first-choice-alert']")).to_be_visible()
        expect(page.locator("[data-testid='no-reliable-first-choice-alert']")).to_contain_text("无可靠首次选择记录")

        client.close()

    return {
        "status": "passed",
        "artifacts": harness.artifacts,
        "details": {
            "telemetry_gap_barrier_verified": True,
            "telemetry_incomplete_flag_verified": True,
            "no_reliable_first_choice_display_verified": True,
        },
    }
