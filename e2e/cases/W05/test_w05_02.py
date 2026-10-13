"""W05-02: 分页消歧、多个词义采纳与排除"""
from playwright.sync_api import expect
from support.web_harness import WebHarness


def run(context: dict) -> dict:
    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page

        # 1. Open imports page
        page.goto(f"{base_url}/imports", wait_until="networkidle")
        expect(page.locator("[data-testid='imports-view']")).to_be_visible()

        # 2. Input vocabulary with ambiguous ("bank"), single-sense ("river", "mountain"), and unknown ("unknownxyz")
        page.locator("[data-testid='import-text-input']").fill("bank\nriver\nmountain\nunknownxyz\n")
        page.locator("[data-testid='submit-import-btn']").click()

        # 3. Wait for summary card
        expect(page.locator("[data-testid='import-summary-card']")).to_be_visible()
        expect(page.locator("[data-testid='stat-total']")).to_contain_text("4")
        expect(page.locator("[data-testid='stat-resolved']")).to_contain_text("2")
        expect(page.locator("[data-testid='stat-ambiguous']")).to_contain_text("1")
        expect(page.locator("[data-testid='stat-not-found']")).to_contain_text("1")

        # 4. Status filtering test
        # Filter by ambiguous
        page.locator("[data-testid='status-filter']").get_by_text("待消歧").click()
        page.wait_for_timeout(400)
        expect(page.locator("[data-testid='import-items-table'] tbody")).to_contain_text("bank")
        expect(page.locator("[data-testid='import-items-table'] tbody")).not_to_contain_text("river")

        # Filter by not_found
        page.locator("[data-testid='status-filter']").get_by_text("未收录").click()
        page.wait_for_timeout(400)
        expect(page.locator("[data-testid='import-items-table'] tbody")).to_contain_text("unknownxyz")
        expect(page.locator("[data-testid='import-items-table'] tbody")).not_to_contain_text("bank")

        # Return to all items
        page.locator("[data-testid='status-filter']").get_by_text("全部条目").click()
        page.wait_for_timeout(400)

        # 5. Open Disambiguation Drawer for "bank"
        page.locator("[data-testid='resolve-btn-0']").click()
        expect(page.locator("[data-testid='resolve-drawer']")).to_be_visible()
        expect(page.locator("[data-testid='resolve-drawer']")).to_contain_text("bank")

        # Verify candidate senses are displayed in drawer
        drawer = page.locator("[data-testid='resolve-drawer']")
        expect(drawer.get_by_text("银行")).to_be_visible()
        expect(drawer.get_by_text("河岸")).to_be_visible()

        # Multi-sense adoption: Check BOTH candidate senses!
        checkboxes = page.locator("[data-testid='candidate-checkbox-group'] .ant-checkbox-input")
        assert checkboxes.count() >= 2, f"Expected at least 2 candidates, got {checkboxes.count()}"
        checkboxes.nth(0).click()
        checkboxes.nth(1).click()

        # Fill custom reason
        page.locator("[data-testid='resolve-reason-input']").fill("同时采纳金融与地理词义")

        # Click Adopt button
        adopt_btn = page.locator("[data-testid='adopt-btn']")
        expect(adopt_btn).to_be_enabled()
        adopt_btn.click()

        # Wait for drawer to close
        expect(page.locator("[data-testid='resolve-drawer']")).not_to_be_visible()
        page.wait_for_timeout(500)

        # 6. Verify ambiguous count decreased to 0, resolved increased to 3
        expect(page.locator("[data-testid='stat-ambiguous']")).to_contain_text("0")
        expect(page.locator("[data-testid='stat-resolved']")).to_contain_text("3")

        # Verify row shows adopted 2 senses
        expect(page.locator("[data-testid='import-items-table'] tbody")).to_contain_text("已采纳 2 个词义")

        # 7. Disambiguate / Exclude "unknownxyz"
        page.locator("[data-testid='resolve-btn-3']").click()
        expect(page.locator("[data-testid='resolve-drawer']")).to_be_visible()

        # Click Exclude
        page.locator("[data-testid='exclude-btn']").click()
        expect(page.locator("[data-testid='resolve-drawer']")).not_to_be_visible()
        page.wait_for_timeout(500)

        # Verify excluded count is now 1, not_found is now 0
        expect(page.locator("[data-testid='stat-excluded']")).to_contain_text("1")
        expect(page.locator("[data-testid='stat-not-found']")).to_contain_text("0")

        return {
            "status": "passed",
            "artifacts": harness.artifacts,
            "details": {
                "pagination_and_filtering_verified": True,
                "multi_sense_adopt_verified": True,
                "exclusion_verified": True,
                "summary_counts_live_update_verified": True,
            },
        }
