"""W03-06: 结果 null、void、首次记录缺失与解析"""
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

        # 1. Create an exam via API and submit it with 1 answered, 2 omitted
        client = APIClient(base_url)
        client.initialize_client_context()
        create_resp = client.post("/api/v1/exams", json={
            "bank_id": BANK_ID,
            "question_count": 3
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert create_resp.status_code == 201
        exam_data = create_resp.json()
        exam_id = exam_data["exam_id"]
        q0 = exam_data["public_questions"][0]

        # Answer question 0
        ev = {
            "instance_id": q0["instance_id"],
            "action": "select",
            "option_id": q0["options"][0]["id"],
            "client_event_id": str(uuid.uuid4()),
            "client_sequence": 1,
            "expected_revision": 0
        }
        resp_ev = client.post(f"/api/v1/exams/{exam_id}/response-events", json=ev, headers={"Idempotency-Key": ev["client_event_id"]})
        assert resp_ev.status_code == 200

        # Submit exam
        sub_key = str(uuid.uuid4())
        resp_sub = client.post(f"/api/v1/exams/{exam_id}/submit", json={
            "expected_revision": 1,
            "submission_id": sub_key,
            "last_ack_sequence": 1
        }, headers={"Idempotency-Key": sub_key})
        assert resp_sub.status_code == 200

        # 2. Open Result page in browser
        page.goto(f"{base_url}/exams/{exam_id}/result", wait_until="networkidle")
        expect(page.locator("[data-testid='exam-result-view']")).to_be_visible()

        # Check score statistics
        expect(page.locator("[data-testid='ratio-stat']")).to_be_visible()
        expect(page.locator("[data-testid='score-stat']")).to_be_visible()
        expect(page.locator("[data-testid='omitted-stat']")).to_contain_text("2")

        # Check question cards and explanation
        expect(page.locator("[data-testid='result-question-0']")).to_be_visible()
        expect(page.locator("[data-testid='explanation-0']")).to_be_visible()

        # 3. Test null ratio display: update assessment_result to ratio = NULL
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("UPDATE assessment_result SET ratio = NULL WHERE session_id = ?", (exam_id,))
            conn.commit()

        page.reload(wait_until="networkidle")
        expect(page.locator("[data-testid='exam-result-view']")).to_be_visible()

        # Must display "暂无有效题目" instead of 0% or NaN
        expect(page.locator("[data-testid='ratio-stat']")).to_contain_text("暂无有效题目")

        # 4. Test reliable_first_choices = 0 / missing telemetry display
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE assessment_item_result
                SET first_correct = NULL
                WHERE assessment_id = (SELECT id FROM assessment_result WHERE session_id = ?)
                """,
                (exam_id,)
            )
            conn.commit()

        page.reload(wait_until="networkidle")
        expect(page.locator("[data-testid='no-reliable-first-choice-alert']")).to_be_visible()
        expect(page.locator("[data-testid='no-reliable-first-choice-alert']")).to_contain_text("无可靠首次选择记录")

        client.close()

    return {
        "status": "passed",
        "artifacts": harness.artifacts,
        "details": {
            "result_page_statistics_verified": True,
            "null_ratio_displays_no_valid_questions": True,
            "missing_telemetry_displays_no_reliable_first_choice": True,
            "question_explanations_verified": True,
        },
    }
