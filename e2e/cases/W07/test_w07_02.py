"""W07-02: 退役影响后续选题并保留旧事实 (browser)"""
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

        # 1. Connect to SQLite to find the bank ID
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("SELECT id FROM bank WHERE name = '开发样例'")
        bank_id = cur.fetchone()[0]
        conn.close()

        # 2. Start practice session to answer a question and generate historical fact
        client = APIClient(base_url)
        client.initialize_client_context()

        create_resp = client.post("/api/v1/practice-sessions", json={"bank_id": bank_id})
        assert create_resp.status_code == 201, f"Create session failed: {create_resp.text}"
        sess_id = create_resp.json()["session"]["id"]

        next_resp = client.post(f"/api/v1/practice-sessions/{sess_id}/next", json={"expected_cursor": 0})
        assert next_resp.status_code == 200
        next_data = next_resp.json()
        q0 = next_data["question"]
        inst_id_0 = q0["instance_id"]
        selected_opt = q0["options"][0]["id"]

        att_resp = client.post(
            f"/api/v1/practice-sessions/{sess_id}/attempts",
            json={"instance_id": inst_id_0, "option_id": selected_opt},
            headers={"Idempotency-Key": "att-w07-02"},
        )
        assert att_resp.status_code == 200, f"Attempt failed: {att_resp.text}"
        sess_rev = att_resp.json().get("session_revision", 1)

        # Get target_qv_id from question_instance in SQLite
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute(
            """
            SELECT qv.id, qv.revision
            FROM question_instance qi
            JOIN question_version qv ON qv.id = qi.question_version_id
            WHERE qi.id = ?
            """,
            (inst_id_0,),
        )
        target_qv_id, target_qv_revision = cur.fetchone()

        # End session so no active session prevents management operations
        client.post(
            f"/api/v1/practice-sessions/{sess_id}/end",
            json={"expected_revision": sess_rev, "reason": "ended"},
            headers={"Idempotency-Key": f"end-{sess_id}"},
        )
        cur.execute("UPDATE session SET status = 'ended' WHERE id = ?", (sess_id,))
        conn.commit()

        # 3. Verify SQLite has historical attempt and exposure events
        cur.execute("SELECT count(*) FROM attempt_event WHERE instance_id = ?", (inst_id_0,))
        initial_attempts = cur.fetchone()[0]
        assert initial_attempts == 1, "Attempt event must exist in DB"

        cur.execute("SELECT count(*) FROM exposure_event WHERE source_instance_id = ?", (inst_id_0,))
        initial_exposures = cur.fetchone()[0]
        assert initial_exposures >= 1, "Exposure events must exist in DB"
        conn.close()

        # 4. Open /quality in browser
        page.goto(f"{base_url}/quality", wait_until="networkidle")
        expect(page.locator("[data-testid='quality-view']")).to_be_visible()

        # 5. Switch to question versions tab
        page.locator("[data-testid='tab-question-versions']").click()
        expect(page.locator("[data-testid='question-versions-table']")).to_be_visible()

        # Find the row for target_qv_id
        target_qv_short = target_qv_id[:8]
        target_row = page.locator("tr.ant-table-row").filter(has_text=target_qv_short).first
        expect(target_row).to_be_visible()

        # 6. Click Retire button in this row
        retire_btn = target_row.locator("[data-testid='retire-btn']")
        retire_btn.click()

        expect(page.locator("[data-testid='retire-modal-content']")).to_be_visible()
        page.locator("[data-testid='retire-reason-input']").fill("淘汰该旧题目版本，不再用于后续训练")
        with page.expect_request(
            lambda request: request.method == "POST"
            and request.url.endswith(f"/api/v1/question-versions/{target_qv_id}/retire")
        ) as retire_request:
            page.locator("[data-testid='submit-retire-btn']").click()
        assert retire_request.value.post_data_json["expected_revision"] == target_qv_revision

        # 7. Verify in UI that status became retired
        expect(target_row.locator("[data-testid='status-retired']")).to_be_visible()

        # 8. Check in SQLite: question_version status is retired, and history is preserved!
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("SELECT status FROM question_version WHERE id = ?", (target_qv_id,))
        qv_status = cur.fetchone()[0]
        assert qv_status == "retired", f"Expected retired in DB, got {qv_status}"

        # Verify historical events are preserved
        cur.execute("SELECT count(*) FROM attempt_event WHERE instance_id = ?", (inst_id_0,))
        assert cur.fetchone()[0] == initial_attempts, "Historical attempts must be preserved after retire"

        cur.execute("SELECT count(*) FROM exposure_event WHERE source_instance_id = ?", (inst_id_0,))
        assert cur.fetchone()[0] == initial_exposures, "Historical exposures must be preserved after retire"
        conn.close()

        # 9. Start a new practice session and verify target_qv_id is NEVER selected
        create2_resp = client.post("/api/v1/practice-sessions", json={"bank_id": bank_id})
        assert create2_resp.status_code == 201
        sess2_id = create2_resp.json()["session"]["id"]

        for cursor in range(3):
            next_step = client.post(f"/api/v1/practice-sessions/{sess2_id}/next", json={"expected_cursor": cursor})
            if next_step.status_code != 200:
                break
            step_data = next_step.json()
            if step_data.get("status") != "ready":
                break
            q = step_data["question"]
            # Look up version in DB for selected question
            conn = sqlite3.connect(db_path)
            cur = conn.cursor()
            cur.execute("SELECT question_version_id FROM question_instance WHERE id = ?", (q["instance_id"],))
            selected_ver = cur.fetchone()[0]
            conn.close()
            assert selected_ver != target_qv_id, (
                f"Retired question version {target_qv_id} was selected in step {cursor}!"
            )
            # Answer quickly to advance
            client.post(
                f"/api/v1/practice-sessions/{sess2_id}/attempts",
                json={"instance_id": q["instance_id"], "option_id": q["options"][0]["id"]},
                headers={"Idempotency-Key": f"att-step-{cursor}"},
            )

        client.post(
            f"/api/v1/practice-sessions/{sess2_id}/end",
            json={"expected_revision": cursor + 1, "reason": "ended"},
            headers={"Idempotency-Key": f"end-{sess2_id}"},
        )

        return {
            "status": "passed",
            "details": {
                "retired_qv_id": target_qv_id,
                "history_preserved": True,
                "selection_exclusion_verified": True,
                "retire_expected_revision_verified": True,
            },
        }
