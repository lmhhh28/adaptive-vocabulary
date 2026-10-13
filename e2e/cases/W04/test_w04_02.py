"""W04-02: 详情、成员变化、CAS与归档"""
import sqlite3
from contextlib import closing
import httpx
from playwright.sync_api import expect
from support.web_harness import WebHarness

BANK_ID = "00000000-0000-4000-8000-000000000802"


def run(context: dict) -> dict:
    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page
        db_path = harness.data_dir / "app.db"

        # 1. Open bank detail page
        page.goto(f"{base_url}/banks/{BANK_ID}", wait_until="networkidle")
        expect(page.locator("[data-testid='bank-detail-view']")).to_be_visible()

        # Check initial revision and status
        expect(page.locator("[data-testid='bank-status-tag']")).to_contain_text("正常")
        expect(page.locator("[data-testid='bank-revision-tag']")).to_contain_text("rev 0")

        # 2. Check members table
        expect(page.locator("[data-testid='members-table']")).to_be_visible()

        # Select first member checkbox in table
        first_checkbox = page.locator("[data-testid='members-table'] tbody tr:first-child .ant-checkbox-input")
        first_checkbox.click()
        page.wait_for_timeout(300)

        # 3. Click remove selected members
        remove_btn = page.locator("[data-testid='remove-members-btn']")
        expect(remove_btn).to_be_enabled()
        remove_btn.click()
        page.wait_for_timeout(600)

        # 4. Verify revision incremented to rev 1
        expect(page.locator("[data-testid='bank-revision-tag']")).to_contain_text("rev 1")

        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT revision, archived FROM bank WHERE id = ?", (BANK_ID,))
            rev, arch = cur.fetchone()
            assert rev == 1, f"Expected revision 1 after removal, got {rev}"
            assert arch == 0

        # 5. CAS conflict test: attempt to archive with stale expected_revision = 0
        client = httpx.Client(base_url=base_url)
        csrf_resp = client.get("/client-context")
        csrf_token = csrf_resp.json().get("csrf_token", "")

        stale_archive_resp = client.post(
            f"/api/v1/banks/{BANK_ID}/archive",
            headers={
                "Idempotency-Key": "cas-conflict-test-key",
                "X-CSRF-Token": csrf_token,
            },
            json={"expected_revision": 0},
        )
        assert stale_archive_resp.status_code == 409, f"Expected 409 conflict, got {stale_archive_resp.status_code}"
        conflict_data = stale_archive_resp.json()
        assert conflict_data.get("error", {}).get("code") == "REVISION_CONFLICT"
        assert conflict_data.get("error", {}).get("details", {}).get("current_revision") == 1

        # 6. Archive bank via UI
        archive_btn = page.locator("[data-testid='archive-bank-btn']")
        expect(archive_btn).to_be_visible()
        archive_btn.click()

        confirm_btn = page.get_by_role("button", name="确认归档")
        expect(confirm_btn).to_be_visible()
        confirm_btn.click()
        page.wait_for_timeout(600)

        # 7. Verify status tag changed to "已归档"
        expect(page.locator("[data-testid='bank-status-tag']")).to_contain_text("已归档")

        # 8. Verify "开始练习" and "新建考试" buttons are disabled
        expect(page.locator("[data-testid='detail-practice-btn']")).to_be_disabled()
        expect(page.locator("[data-testid='detail-exam-btn']")).to_be_disabled()

        # 9. Verify in database
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT revision, archived FROM bank WHERE id = ?", (BANK_ID,))
            final_rev, final_arch = cur.fetchone()
            assert final_arch == 1, f"Expected archived = 1, got {final_arch}"
            assert final_rev == 2, f"Expected revision = 2, got {final_rev}"

        return {
            "status": "passed",
            "artifacts": harness.artifacts,
            "details": {
                "member_removed": True,
                "cas_conflict_verified": True,
                "bank_archived": True,
                "actions_disabled": True,
            },
        }
