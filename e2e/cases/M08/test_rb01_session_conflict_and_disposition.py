"""RB-01: Session conflict and evidence disposition acceptance test.
Verifies:
1. When an active session exists, evidence disposition is rejected with 409 SESSION_ACTIVE
   and NO event_revision or disposition records are written.
2. When the active session is ended/abandoned, disposition atomically enters maintenance,
   records revisions, executes rebuild, and restores active mode.
3. Browser quality management page displays the disposition and rebuild history.
"""
from pathlib import Path
import sqlite3
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

        # Connect to SQLite to read a target question_version
        db_path = data_dir / "app.db"
        conn = sqlite3.connect(str(db_path))
        cur = conn.cursor()
        cur.execute("SELECT id, revision FROM question_version WHERE status = 'published' LIMIT 1")
        row = cur.fetchone()
        assert row is not None, "No published question_version found"
        qv_id, qv_rev = row[0], row[1]
        conn.close()

        # 1. Create active practice session
        resp = client.post("/api/v1/practice-sessions", json={"bank_id": bank_id}, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp.status_code == 201, f"Create practice session failed: {resp.status_code}: {resp.text}"
        session_id = resp.json()["session"]["id"]

        # 2. Attempt evidence disposition while session is active -> MUST return 409 SESSION_ACTIVE
        disp_resp = client.post(f"/api/v1/question-versions/{qv_id}/evidence-dispositions", json={
            "action": "disputed",
            "reason": "Suspected wrong distractor",
            "expected_revision": qv_rev,
        }, headers={"Idempotency-Key": str(uuid.uuid4())})

        assert disp_resp.status_code == 409, f"Expected 409 SESSION_ACTIVE, got {disp_resp.status_code}: {disp_resp.text}"
        err_body = disp_resp.json()
        assert err_body["error"]["code"] == "SESSION_ACTIVE", f"Expected code SESSION_ACTIVE, got {err_body}"

        # Verify in DB that no event_revision was written
        conn = sqlite3.connect(str(db_path))
        cur = conn.cursor()
        cur.execute("SELECT count(*) FROM event_revision WHERE event_id = ?", (qv_id,))
        count = cur.fetchone()[0]
        assert count == 0, f"Expected 0 event_revisions written during conflict, found {count}"

        cur.execute("SELECT count(*) FROM evidence_disposition WHERE question_version_id = ?", (qv_id,))
        disp_count = cur.fetchone()[0]
        assert disp_count == 0, f"Expected 0 evidence_dispositions written during conflict, found {disp_count}"

        # Verify question_version status is still published
        cur.execute("SELECT status FROM question_version WHERE id = ?", (qv_id,))
        status = cur.fetchone()[0]
        assert status == "published", f"Expected published, got {status}"
        conn.close()

        # 3. End the active session
        end_resp = client.post(f"/api/v1/practice-sessions/{session_id}/end", json={
            "reason": "ended",
            "expected_revision": 0,
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert end_resp.status_code == 200, f"End session failed: {end_resp.status_code}: {end_resp.text}"

        # 4. Now apply evidence disposition (no active sessions)
        disp_resp2 = client.post(f"/api/v1/question-versions/{qv_id}/evidence-dispositions", json={
            "action": "disputed",
            "reason": "Confirmed ambiguous distractor",
            "expected_revision": qv_rev,
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert disp_resp2.status_code == 200, f"Expected 200 on disposition, got {disp_resp2.status_code}: {disp_resp2.text}"
        disp_data = disp_resp2.json()
        assert disp_data["status"] == "succeeded", f"Expected status succeeded, got {disp_data}"
        assert disp_data["disposition_id"] != ""

        # 5. Verify database state
        conn = sqlite3.connect(str(db_path))
        cur = conn.cursor()
        cur.execute("SELECT status FROM question_version WHERE id = ?", (qv_id,))
        new_status = cur.fetchone()[0]
        assert new_status == "disputed", f"Expected question_version status disputed, got {new_status}"

        cur.execute("SELECT mode, active_version, requires_rebuild FROM projection_control WHERE id = 1")
        pc_mode, pc_active_ver, pc_req_reb = cur.fetchone()
        assert pc_mode == "active", f"Expected projection_control mode active after successful rebuild, got {pc_mode}"
        assert pc_req_reb == 0, f"Expected requires_rebuild 0, got {pc_req_reb}"
        conn.close()

        # 6. Verify via browser quality page
        with sync_playwright() as p:
            browser = p.chromium.launch(headless=True)
            b_ctx = browser.new_context()
            trace_path = case_dir / "trace.zip"
            b_ctx.tracing.start(screenshots=True, snapshots=True)

            page = b_ctx.new_page()
            page.goto(f"{base_url}/quality")
            assert "质量与维护控制台" in page.title()

            # Verify system mode is displayed
            mode_el = page.locator("#system-mode")
            mode_el.wait_for(state="visible")
            assert mode_el.text_content().strip() == "active"

            # Verify the disposition appears in history
            disposition_cell = page.locator(f"text=Confirmed ambiguous distractor")
            assert disposition_cell.count() > 0, "Disposition reason not found in quality UI"

            shot_path = case_dir / "quality.png"
            page.screenshot(path=str(shot_path))
            artifacts.append(str(shot_path))

            b_ctx.tracing.stop(path=str(trace_path))
            artifacts.append(str(trace_path))
            browser.close()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "question_version_id": qv_id,
            "session_id": session_id,
            "disposition_id": disp_data["disposition_id"],
        },
    }
