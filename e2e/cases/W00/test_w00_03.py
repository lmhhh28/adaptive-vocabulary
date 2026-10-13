"""W00-03: outbox 重开/存储不可用与共享组件键盘"""
import time
from playwright.sync_api import expect
from support.web_harness import WebHarness


def run(context: dict) -> dict:
    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page

        page.goto(f"{base_url}/components", wait_until="networkidle")
        expect(page.locator("[data-testid='components-demo-view']")).to_be_visible()

        # 1. Outbox durability & reopen test
        expect(page.locator("[data-testid='enqueue-outbox-btn']")).to_be_visible()
        page.locator("[data-testid='enqueue-outbox-btn']").click()
        page.wait_for_timeout(300)
        expect(page.locator("[data-testid='outbox-items-list']")).to_contain_text("op-")

        # Reload page and check that outbox item persisted in IndexedDB
        page.reload(wait_until="networkidle")
        page.locator("[data-testid='refresh-outbox-btn']").click()
        page.wait_for_timeout(300)
        expect(page.locator("[data-testid='outbox-items-list']")).to_contain_text("op-")
        expect(page.locator("[data-testid='sync-pending-tag']")).to_be_visible()

        # 2. Outbox storage fault injection test
        page.locator("[data-testid='toggle-storage-fault']").click()
        page.wait_for_timeout(200)
        expect(page.locator("[data-testid='storage-unavailable-tag']")).to_be_visible()

        page.locator("[data-testid='enqueue-outbox-btn']").click()
        page.wait_for_timeout(300)
        expect(page.locator("[data-testid='outbox-error-msg']")).to_be_visible()
        expect(page.locator("[data-testid='outbox-error-msg']")).to_contain_text("StorageUnavailableError")

        # Disable fault and clean up
        page.locator("[data-testid='toggle-storage-fault']").click()
        page.wait_for_timeout(200)
        page.locator("[data-testid='clear-outbox-btn']").click()
        page.wait_for_timeout(300)
        expect(page.locator("[data-testid='sync-ok-tag']")).to_be_visible()

        # 3. PracticeOptions keyboard shortcuts test
        # Press '1' to answer with Option A
        page.keyboard.press("1")
        page.wait_for_timeout(600)
        expect(page.locator("[data-testid='feedback-panel']")).to_be_visible()
        expect(page.locator("[data-testid='feedback-panel']")).to_contain_text("回答正确")

        # Reset question state
        page.get_by_role("button", name="重置作答态").click()
        expect(page.locator("[data-testid='feedback-panel']")).not_to_be_visible()

        # Press '2' to answer with Option B
        page.keyboard.press("2")
        page.wait_for_timeout(600)
        expect(page.locator("[data-testid='feedback-panel']")).to_be_visible()
        expect(page.locator("[data-testid='feedback-panel']")).to_contain_text("回答错误")

        # 4. AnswerSheet navigation & return to current
        expect(page.locator("[data-testid='answer-sheet']")).to_be_visible()
        page.locator("[data-testid='sheet-item-8']").click()
        expect(page.locator("[data-testid='return-current-btn']")).to_be_visible()

        page.locator("[data-testid='return-current-btn']").click()
        expect(page.locator("[data-testid='return-current-btn']")).not_to_be_visible()

        return {
            "status": "passed",
            "artifacts": harness.artifacts,
            "details": {
                "outbox_reopen_verified": True,
                "outbox_fault_verified": True,
                "keyboard_shortcuts_verified": True,
                "answer_sheet_navigation_verified": True,
            },
        }
