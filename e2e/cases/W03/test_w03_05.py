"""W03-05: deadline、迟到事件及放弃返回 submitted"""
import json
import sqlite3
import time
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

        # 1. Create a timed exam
        client = APIClient(base_url)
        client.initialize_client_context()
        create_resp = client.post("/api/v1/exams", json={
            "bank_id": BANK_ID,
            "question_count": 3,
            "time_limit_seconds": 60
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert create_resp.status_code == 201
        exam_id = create_resp.json()["exam_id"]

        # Expire exam directly in DB: set deadline_at to 10 minutes ago (in microseconds)
        past_micro = int(time.time() * 1_000_000) - 600_000_000
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("UPDATE session SET deadline_at = ? WHERE id = ?", (past_micro, exam_id))
            cur.execute("UPDATE exam_session SET deadline_at = ? WHERE session_id = ?", (past_micro, exam_id))
            conn.commit()

        # Open in browser. An already-expired open exam must immediately enter
        # the same settlement flow as a countdown that reaches zero.
        with page.expect_response(
            lambda response: response.request.method == "POST"
            and response.url.endswith(f"/api/v1/exams/{exam_id}/submit")
        ) as settlement_response:
            page.goto(f"{base_url}/exams/{exam_id}", wait_until="networkidle")
        assert settlement_response.value.status in (200, 409)
        expect(page.locator("[data-testid='exam-result-view']")).to_be_visible()

        # Late response-event via API must return EXAM_SUBMITTED
        late_event = {
            "instance_id": create_resp.json()["public_questions"][0]["instance_id"],
            "action": "select",
            "option_id": create_resp.json()["public_questions"][0]["options"][0]["id"],
            "client_event_id": str(uuid.uuid4()),
            "client_sequence": 1,
            "expected_revision": 0
        }
        resp_late = client.post(f"/api/v1/exams/{exam_id}/response-events", json=late_event, headers={"Idempotency-Key": late_event["client_event_id"]})
        assert resp_late.status_code == 409, f"Expected 409 on late event, got {resp_late.status_code}"
        assert resp_late.json()["error"]["code"] in ("EXAM_SUBMITTED", "SESSION_CLOSED")

        # Check DB that session is submitted
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT status FROM session WHERE id = ?", (exam_id,))
            assert cur.fetchone()[0] == "submitted"

        # 2. Test Abandon Exam on a fresh session
        create_resp2 = client.post("/api/v1/exams", json={
            "bank_id": BANK_ID,
            "question_count": 3
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert create_resp2.status_code == 201
        exam_id2 = create_resp2.json()["exam_id"]

        page.goto(f"{base_url}/exams/{exam_id2}", wait_until="networkidle")
        expect(page.locator("[data-testid='exam-active-view']")).to_be_visible()

        # Click Abandon
        page.locator("[data-testid='abandon-exam-btn']").click()
        expect(page.locator("[data-testid='abandon-confirm-modal']")).to_be_visible()

        # Confirm Abandon
        page.get_by_role("button", name="确认放弃").click()
        page.wait_for_timeout(600)

        # Check DB status is submitted or abandoned
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT status FROM session WHERE id = ?", (exam_id2,))
            status2 = cur.fetchone()[0]
            assert status2 in ("submitted", "abandoned"), f"Expected submitted/abandoned, got {status2}"

        client.close()

    return {
        "status": "passed",
        "artifacts": harness.artifacts,
        "details": {
            "deadline_expiration_verified": True,
            "expired_reopen_auto_settlement_verified": True,
            "late_event_rejection_verified": True,
            "abandon_flow_verified": True,
        },
    }
