"""W04-04: 活动会话保护和维护禁写"""
import sqlite3
from contextlib import closing
import httpx
from playwright.sync_api import expect
from support.web_harness import WebHarness

BANK_ID = "00000000-0000-4000-8000-000000000801"


def run(context: dict) -> dict:
    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page
        db_path = harness.data_dir / "app.db"

        # 1. Insert an active session into SQLite
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute(
                "INSERT INTO session (id, bank_id, kind, status, created_at, updated_at) "
                "VALUES ('active-sess-w04', ?, 'practice', 'open', 1000, 1000)",
                (BANK_ID,),
            )
            conn.commit()

        # 2. Open bank detail page in browser
        page.goto(f"{base_url}/banks/{BANK_ID}", wait_until="networkidle")

        # 3. Assert active session lock view is displayed
        expect(page.locator("[data-testid='bank-detail-locked-view']")).to_be_visible()
        expect(page.locator("[data-testid='session-active-lock-alert']")).to_be_visible()

        # 4. Verify API endpoints return 409 SESSION_ACTIVE
        client = httpx.Client(base_url=base_url)

        bank_resp = client.get(f"/api/v1/banks/{BANK_ID}")
        assert bank_resp.status_code == 409, f"Expected 409 for bank detail, got {bank_resp.status_code}"
        assert bank_resp.json().get("error", {}).get("code") == "SESSION_ACTIVE"

        sense_resp = client.get(f"/api/v1/content/sense-versions?bank_id={BANK_ID}")
        assert sense_resp.status_code == 409, f"Expected 409 for sense versions, got {sense_resp.status_code}"
        assert sense_resp.json().get("error", {}).get("code") == "SESSION_ACTIVE"

        q_resp = client.get(f"/api/v1/content/question-versions?bank_id={BANK_ID}")
        assert q_resp.status_code == 409, f"Expected 409 for question versions, got {q_resp.status_code}"
        assert q_resp.json().get("error", {}).get("code") == "SESSION_ACTIVE"

        # 5. Clear active session
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("UPDATE session SET status = 'ended' WHERE id = 'active-sess-w04'")
            conn.commit()

        # 6. Reload page - should now show normal detail view
        page.reload(wait_until="networkidle")
        expect(page.locator("[data-testid='bank-detail-view']")).to_be_visible()

        # 7. Enable maintenance mode in SQLite
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("UPDATE projection_control SET mode = 'rebuilding'")
            conn.commit()

        # 8. Obtain CSRF token
        csrf_resp = client.get("/client-context")
        csrf_token = csrf_resp.json().get("csrf_token", "")
        auth_headers = {
            "X-CSRF-Token": csrf_token,
            "Idempotency-Key": "maint-block-test-key",
        }

        # 9. Verify write endpoints are blocked with 503 MAINTENANCE
        # POST /api/v1/banks
        create_bank_resp = client.post(
            "/api/v1/banks",
            headers=auth_headers,
            json={"name": "Maint Bank", "note": "forbidden"},
        )
        assert create_bank_resp.status_code == 503, f"Expected 503, got {create_bank_resp.status_code}"
        assert create_bank_resp.json().get("error", {}).get("code") == "MAINTENANCE"

        # POST /api/v1/sources
        create_source_resp = client.post(
            "/api/v1/sources",
            headers={**auth_headers, "Idempotency-Key": "maint-source-key"},
            json={
                "name": "Maint Source",
                "location": "loc",
                "source_version": "v1",
                "license": "MIT",
                "usage_scope": "all",
            },
        )
        assert create_source_resp.status_code == 503, f"Expected 503, got {create_source_resp.status_code}"
        assert create_source_resp.json().get("error", {}).get("code") == "MAINTENANCE"

        # POST /api/v1/banks/{BANK_ID}/archive
        archive_resp = client.post(
            f"/api/v1/banks/{BANK_ID}/archive",
            headers={**auth_headers, "Idempotency-Key": "maint-archive-key"},
            json={"expected_revision": 0},
        )
        assert archive_resp.status_code == 503, f"Expected 503, got {archive_resp.status_code}"
        assert archive_resp.json().get("error", {}).get("code") == "MAINTENANCE"

        # POST /api/v1/banks/{BANK_ID}/members/remove
        remove_resp = client.post(
            f"/api/v1/banks/{BANK_ID}/members/remove",
            headers={**auth_headers, "Idempotency-Key": "maint-remove-key"},
            json={"expected_revision": 0, "sense_ids": ["some-sense-id"]},
        )
        assert remove_resp.status_code == 503, f"Expected 503, got {remove_resp.status_code}"
        assert remove_resp.json().get("error", {}).get("code") == "MAINTENANCE"

        return {
            "status": "passed",
            "artifacts": harness.artifacts,
            "details": {
                "session_active_protection_verified": True,
                "detail_locked_on_session": True,
                "discovery_locked_on_session": True,
                "maintenance_write_lock_verified": True,
            },
        }
