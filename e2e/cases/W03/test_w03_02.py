"""W03-02: unknown/rebase/顺序水位和同一事件重试"""
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

        # 1. Create an exam via API first
        client = APIClient(base_url)
        client.initialize_client_context()
        create_resp = client.post("/api/v1/exams", json={
            "bank_id": BANK_ID,
            "question_count": 3
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert create_resp.status_code == 201, f"Failed to create exam: {create_resp.text}"
        exam_data = create_resp.json()
        exam_id = exam_data["exam_id"]
        q0 = exam_data["public_questions"][0]
        q1 = exam_data["public_questions"][1]

        # 2. Open the exam in browser while session is at revision 0
        page.goto(f"{base_url}/exams/{exam_id}", wait_until="networkidle")
        expect(page.locator("[data-testid='exam-active-view']")).to_be_visible()
        expect(page.locator("[data-testid='exam-question-card']")).to_be_visible()

        # 3. Simulate concurrent revision bump on backend (Tab B creates event on revision 0)
        client_event_id_concurrent = str(uuid.uuid4())
        ev_concurrent = {
            "instance_id": q0["instance_id"],
            "action": "select",
            "option_id": q0["options"][0]["id"],
            "client_event_id": client_event_id_concurrent,
            "client_sequence": 1,
            "expected_revision": 0
        }
        resp_c = client.post(f"/api/v1/exams/{exam_id}/response-events", json=ev_concurrent, headers={"Idempotency-Key": client_event_id_concurrent})
        assert resp_c.status_code == 200, f"Concurrent event failed: {resp_c.text}"
        assert resp_c.json()["current_revision"] == 1

        # 4. User in browser makes a selection on question 0 (option 1)
        # Browser will send with initial expected_revision=0, receive 409 conflict, and auto-rebase!
        page.locator("[data-testid='exam-option-1']").click()
        page.wait_for_timeout(800)

        # Verify in DB that rebase happened and was recorded with rebased_from
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT client_sequence, expected_revision, rebased_from FROM response_event WHERE session_id = ? ORDER BY client_sequence ASC", (exam_id,))
            rows = cur.fetchall()
            assert len(rows) == 2, f"Expected 2 events in DB, got {len(rows)}"
            # First was the concurrent event
            assert rows[0][0] == 1 and rows[0][1] == 0
            # Second was the rebased event from browser: sequence=2, revision=1, rebased_from
            assert rows[1][0] == 2, f"Expected sequence 2, got {rows[1][0]}"
            assert rows[1][1] == 1, f"Expected revision 1, got {rows[1][1]}"
            rebased_info = json.loads(rows[1][2]) if rows[1][2] else {}
            assert rebased_info.get("client_sequence") == 1, f"Expected rebased client_sequence=1, got {rebased_info}"
            assert rebased_info.get("expected_revision") == 0, f"Expected rebased expected_revision=0, got {rebased_info}"

        # 5. Dropped response & idempotent replay test
        # Intercept response events to simulate dropped response after server accepted it
        intercepted = False
        def handle_response_event_route(route, request):
            nonlocal intercepted
            intercepted = True
            _ = route.fetch()
            route.abort("connectionreset")

        page.route("**/api/v1/exams/*/response-events", handle_response_event_route)

        # Switch to question 2 and click option 0
        page.locator("[data-testid='next-question-btn']").click()
        page.wait_for_timeout(200)
        page.locator("[data-testid='exam-option-0']").click()

        page.wait_for_timeout(1000)
        assert intercepted, "Expected event route to be intercepted"

        # Check DB: Server DID receive and record the 3rd event!
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT count(*) FROM response_event WHERE session_id = ?", (exam_id,))
            count_after_drop = cur.fetchone()[0]
            assert count_after_drop == 3, f"Expected 3 events in DB, got {count_after_drop}"

        # Browser should show unknown status before recovery.
        expect(page.locator("[data-testid='sync-unknown-tag']")).to_be_visible()
        expect(page.locator("[data-testid='retry-sync-btn']")).to_be_visible()

        # Reload exactly like a browser restart. Initialization must query the
        # durable receipt and preserve the original request identity/body.
        page.unroute("**/api/v1/exams/*/response-events")
        page.reload(wait_until="networkidle")

        # Status should converge automatically without asking the user to
        # rewrite/rebase an event that the server already accepted.
        expect(page.locator("[data-testid='sync-ok-tag']")).to_be_visible()
        expect(page.locator("[data-testid='retry-sync-btn']")).not_to_be_visible()

        # Check DB: Still exactly 3 events (no duplicate inserted on replay!)
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT count(*) FROM response_event WHERE session_id = ?", (exam_id,))
            count_after_retry = cur.fetchone()[0]
            assert count_after_retry == 3, f"Expected still exactly 3 events, got {count_after_retry}"

            # Check sequence continuity
            cur.execute("SELECT client_sequence FROM response_event WHERE session_id = ? ORDER BY client_sequence ASC", (exam_id,))
            seqs = [r[0] for r in cur.fetchall()]
            assert seqs == [1, 2, 3], f"Expected sequences [1, 2, 3], got {seqs}"

        client.close()

    return {
        "status": "passed",
        "artifacts": harness.artifacts,
        "details": {
            "automatic_rebase_verified": True,
            "dropped_response_reload_receipt_verified": True,
            "sequential_watermark_verified": True,
            "no_duplicate_event_verified": True,
        },
    }
