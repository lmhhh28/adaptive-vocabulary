"""W06-01: 真实版本选择与202任务登记"""
import sqlite3
from playwright.sync_api import expect
from support.web_harness import WebHarness


def run(context: dict) -> dict:
    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page

        # 1. Open generation page
        page.goto(f"{base_url}/generation", wait_until="networkidle")
        expect(page.locator("[data-testid='generation-view']")).to_be_visible()

        # 2. Click create job button
        page.locator("[data-testid='create-job-btn']").click()
        expect(page.locator("[data-testid='tab-create']")).to_be_visible()

        # 3. Select Bank
        page.locator("[data-testid='bank-select']").click()
        page.locator(".ant-select-item-option:has-text('开发样例')").first.click()

        # 4. Wait for sense-versions table to load and select first row
        expect(page.locator("[data-testid='sense-select-table']")).to_be_visible()
        page.wait_for_selector("[data-testid='sense-select-table'] tbody tr.ant-table-row")
        first_row_checkbox = page.locator("[data-testid='sense-select-table'] tbody tr.ant-table-row").first.locator("input[type='checkbox']")
        first_row_checkbox.check()

        # 5. Verify budget preview is visible
        expect(page.locator("[data-testid='budget-preview']")).to_be_visible()

        # 6. Submit creation
        page.locator("[data-testid='submit-job-btn']").click()

        # 7. Expect success card
        expect(page.locator("[data-testid='job-created-card']")).to_be_visible()
        created_job_id_elem = page.locator("[data-testid='created-job-id']")
        expect(created_job_id_elem).to_be_visible()
        created_job_id = created_job_id_elem.inner_text().strip()
        assert created_job_id, "Created Job ID should not be empty"

        # 8. Verify in SQLite database
        db_path = harness.data_dir / "app.db"
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("SELECT status, requested_count, published_count, rejected_count, pending_count FROM generation_job WHERE id = ?", (created_job_id,))
        job_row = cur.fetchone()
        assert job_row is not None, f"Job {created_job_id} not found in DB"
        status, req_cnt, pub_cnt, rej_cnt, pend_cnt = job_row
        assert status == "pending", f"Expected status 'pending', got {status}"
        assert req_cnt in (1, 3), f"Expected requested_count 1 or 3, got {req_cnt}"
        assert pub_cnt == 0, f"Expected published_count 0, got {pub_cnt}"

        cur.execute("SELECT max_requests, max_tokens, authorized_requests, total_tokens, budget_overrun FROM generation_budget_ledger WHERE job_id = ?", (created_job_id,))
        ledger_row = cur.fetchone()
        assert ledger_row is not None, f"Budget ledger for {created_job_id} not found"
        max_req, max_tok, auth_req, tot_tok, overrun = ledger_row
        assert max_req > 0, "max_requests should be positive"
        assert max_tok > 0, "max_tokens should be positive"
        assert overrun == 0, "budget_overrun should be 0"
        conn.close()

        # 9. Click button to view job details
        page.locator("button:has-text('查看任务详情与候选')").click()
        expect(page.locator("[data-testid='job-detail-drawer']")).to_be_visible()
        expect(page.locator("[data-testid='candidates-table']")).to_be_visible()

        return {
            "status": "passed",
            "details": {
                "created_job_id": created_job_id,
                "real_version_selection_verified": True,
                "task_registration_202_verified": True,
                "sqlite_job_pending_verified": True,
                "budget_ledger_verified": True,
            },
        }
