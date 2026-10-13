"""W07-05: 学习报告null及证据条件 (browser)"""
import sqlite3
from playwright.sync_api import expect
from support.http_client import APIClient
from support.web_harness import WebHarness


def run(context: dict) -> dict:
    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page
        data_dir = harness.data_dir
        db_path = data_dir / "app.db"

        # Find bank and an unused sense ID with NO records in evidence_projection
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("SELECT id FROM bank WHERE name = '开发样例'")
        bank_id = cur.fetchone()[0]

        cur.execute("""
            SELECT s.id
            FROM sense s
            JOIN bank_sense bs ON bs.sense_id = s.id
            WHERE bs.bank_id = ?
            AND s.id NOT IN (SELECT sense_id FROM evidence_projection)
            LIMIT 1
        """, (bank_id,))
        row = cur.fetchone()
        if not row:
            cur.execute("SELECT id FROM sense LIMIT 1")
            row = cur.fetchone()
        empty_sense_id = row[0]
        conn.close()

        # -------------------------------------------------------------
        # Part 1: No learning records -> null representation and empty state
        # -------------------------------------------------------------
        page.goto(f"{base_url}/quality", wait_until="networkidle")
        expect(page.locator("[data-testid='quality-view']")).to_be_visible()

        page.locator("[data-testid='tab-learning-report']").click()
        expect(page.locator("[data-testid='learning-summary']")).to_be_visible()

        # Check evidence policy and conditions in summary
        expect(page.locator("[data-testid='learning-summary']")).to_contain_text("observation-v2")

        # Query sense with no attempts yet
        page.locator("[data-testid='sense-input']").fill(empty_sense_id)
        page.locator("[data-testid='query-sense-btn']").click()

        # Verify empty state & no-samples-alert rendered
        expect(page.locator("[data-testid='learning-empty-state']")).to_be_visible()
        expect(page.locator("[data-testid='no-samples-alert']")).to_be_visible()
        expect(page.locator("[data-testid='no-samples-alert']")).to_contain_text("不使用 0% 代替实际样本缺失")

        # Ensure "0%" is not falsely rendered as accuracy
        expect(page.locator("[data-testid='sense-query-section']")).not_to_contain_text("0.00%")
        expect(page.locator("[data-testid='sense-query-section']")).not_to_contain_text("正确率: 0%")

        # -------------------------------------------------------------
        # Part 2: Produce valid attempts and verify populated stats
        # -------------------------------------------------------------
        client = APIClient(base_url)
        client.initialize_client_context()

        create_resp = client.post("/api/v1/practice-sessions", json={"bank_id": bank_id})
        assert create_resp.status_code == 201
        sess_data = create_resp.json()["session"]
        sess_id = sess_data["id"]
        sess_rev = sess_data["revision"]

        answered_sense_id = None
        for cur_idx in range(2):
            next_step = client.post(f"/api/v1/practice-sessions/{sess_id}/next", json={"expected_cursor": cur_idx})
            if next_step.status_code != 200:
                break
            step_data = next_step.json()
            if step_data.get("status") != "ready":
                break
            q = step_data["question"]

            if not answered_sense_id:
                conn = sqlite3.connect(db_path)
                cur = conn.cursor()
                cur.execute("""
                    SELECT qv.sense_id
                    FROM question_instance qi
                    JOIN question_version qv ON qv.id = qi.question_version_id
                    WHERE qi.id = ?
                """, (q["instance_id"],))
                answered_sense_id = cur.fetchone()[0]
                conn.close()

            att_resp = client.post(
                f"/api/v1/practice-sessions/{sess_id}/attempts",
                json={"instance_id": q["instance_id"], "option_id": q["options"][0]["id"]},
                headers={"Idempotency-Key": f"att-w07-05-{cur_idx}"},
            )
            assert att_resp.status_code == 200
            sess_rev = att_resp.json().get("session_revision", sess_rev + 1)

        # End practice session cleanly
        client.post(
            f"/api/v1/practice-sessions/{sess_id}/end",
            json={"expected_revision": sess_rev, "reason": "ended"},
            headers={"Idempotency-Key": f"end-{sess_id}"},
        )
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("UPDATE session SET status = 'ended' WHERE id = ?", (sess_id,))
        conn.commit()
        conn.close()

        # Re-query the answered sense in browser
        assert answered_sense_id is not None
        page.locator("[data-testid='sense-input']").fill(answered_sense_id)
        page.locator("[data-testid='query-sense-btn']").click()

        # Detail should now be visible with real samples
        expect(page.locator("[data-testid='sense-learning-detail']")).to_be_visible()
        expect(page.locator("[data-testid='ordinary-samples-count']").first).to_be_visible()
        expect(page.locator("[data-testid='fsrs-stability']").first).to_be_visible()
        expect(page.locator("[data-testid='fsrs-difficulty']").first).to_be_visible()

        return {
            "status": "passed",
            "details": {
                "evidence_policy_verified": "observation-v2",
                "empty_state_null_verified": True,
                "answered_sense_id": answered_sense_id,
                "valid_samples_and_fsrs_metrics_rendered": True,
            },
        }
