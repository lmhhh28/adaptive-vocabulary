"""W05-04: 导入汇总与题库成员一致"""
import sqlite3
from contextlib import closing
import httpx
from playwright.sync_api import expect
from support.web_harness import WebHarness


def run(context: dict) -> dict:
    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page
        db_path = harness.data_dir / "app.db"

        client = httpx.Client(base_url=base_url)
        csrf_resp = client.get("/client-context")
        csrf_token = csrf_resp.json().get("csrf_token", "")

        # 1. Create a brand new bank with 0 members
        create_resp = client.post(
            "/api/v1/banks",
            headers={
                "X-CSRF-Token": csrf_token,
                "Idempotency-Key": "w05-04-create-bank-key",
            },
            json={
                "name": "W05-04 成员核对题库",
                "note": "验证词表导入、消歧与题库成员严格一致性",
            },
        )
        assert create_resp.status_code == 201
        new_bank_id = create_resp.json()["bank_id"]

        # Verify initial bank has 0 members and rev 0 in DB
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT COUNT(*) FROM bank_sense WHERE bank_id = ?", (new_bank_id,))
            init_members = cur.fetchone()[0]
            assert init_members == 0, f"Expected 0 initial members, got {init_members}"

            cur.execute("SELECT revision FROM bank WHERE id = ?", (new_bank_id,))
            init_rev = cur.fetchone()[0]
            assert init_rev == 0, f"Expected initial rev 0, got {init_rev}"

        # 2. Open imports page with this bank preselected
        page.goto(f"{base_url}/imports?bank_id={new_bank_id}", wait_until="networkidle")
        expect(page.locator("[data-testid='imports-view']")).to_be_visible()
        expect(page.locator("[data-testid='active-bank-tag']")).to_contain_text("W05-04 成员核对题库")

        # 3. Input vocabulary: river (1 sense), mountain (1 sense), bank (2 senses)
        page.locator("[data-testid='import-text-input']").fill("river\nmountain\nbank\n")
        page.locator("[data-testid='submit-import-btn']").click()

        # Wait for summary card
        expect(page.locator("[data-testid='import-summary-card']")).to_be_visible()
        expect(page.locator("[data-testid='stat-total']")).to_contain_text("3")
        expect(page.locator("[data-testid='stat-resolved']")).to_contain_text("2")
        expect(page.locator("[data-testid='stat-ambiguous']")).to_contain_text("1")

        # 4. Disambiguate "bank" and adopt BOTH senses
        page.locator("[data-testid='resolve-btn-2']").click()
        expect(page.locator("[data-testid='resolve-drawer']")).to_be_visible()

        checkboxes = page.locator("[data-testid='candidate-checkbox-group'] .ant-checkbox-input")
        assert checkboxes.count() >= 2
        checkboxes.nth(0).click()
        checkboxes.nth(1).click()

        page.locator("[data-testid='adopt-btn']").click()
        expect(page.locator("[data-testid='resolve-drawer']")).not_to_be_visible()
        page.wait_for_timeout(500)

        # 5. Assert final summary counts
        expect(page.locator("[data-testid='stat-resolved']")).to_contain_text("3")
        expect(page.locator("[data-testid='stat-ambiguous']")).to_contain_text("0")

        # 6. Verify SQLite invariants:
        # river has 1 sense, mountain has 1 sense, bank has 2 senses => total 4 senses in bank_sense!
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT COUNT(*) FROM bank_sense WHERE bank_id = ?", (new_bank_id,))
            final_members = cur.fetchone()[0]
            assert final_members == 4, f"Expected 4 senses in bank_sense, got {final_members}"

            cur.execute("SELECT revision FROM bank WHERE id = ?", (new_bank_id,))
            final_rev = cur.fetchone()[0]
            assert final_rev >= 1, f"Expected bank revision >= 1, got {final_rev}"

        # 7. Navigate to the bank's detail page in the UI
        page.locator("[data-testid='view-bank-btn']").click()
        expect(page.locator("[data-testid='bank-detail-view']")).to_be_visible()
        expect(page.locator("[data-testid='bank-detail-title']")).to_contain_text("W05-04 成员核对题库")

        # Verify member count statistic displays 4
        expect(page.get_by_text("词义成员总数")).to_be_visible()
        expect(page.locator(".ant-statistic-content-value").first).to_contain_text("4")

        # Verify all 4 senses are listed in the members table
        members_table = page.locator("[data-testid='members-table']")
        expect(members_table).to_contain_text("河流")
        expect(members_table).to_contain_text("山峰")
        expect(members_table).to_contain_text("银行")
        expect(members_table).to_contain_text("河岸")

        return {
            "status": "passed",
            "artifacts": harness.artifacts,
            "details": {
                "bank_created": new_bank_id,
                "import_total_entries": 3,
                "disambiguated_and_adopted_both_senses": True,
                "sqlite_bank_sense_count": final_members,
                "bank_detail_ui_verified": True,
            },
        }
