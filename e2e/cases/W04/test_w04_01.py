"""W04-01: 空态到真实题库/来源创建"""
import sqlite3
from contextlib import closing
from playwright.sync_api import expect
from support.web_harness import WebHarness


def run(context: dict) -> dict:
    # Start server with empty seed (no preloaded banks)
    with WebHarness(context, seed="") as harness:
        base_url = harness.base_url
        page = harness.page
        db_path = harness.data_dir / "app.db"

        # 1. Open banks page
        page.goto(f"{base_url}/banks", wait_until="networkidle")
        expect(page.locator("[data-testid='banks-view']")).to_be_visible()

        # 2. Assert empty state is visible
        expect(page.locator("[data-testid='empty-banks']")).to_be_visible()

        # 3. Click create bank button in empty state
        page.locator("[data-testid='empty-create-bank-btn']").click()
        expect(page.locator("[data-testid='bank-name-input']")).to_be_visible()

        # 4. Fill bank creation form
        page.locator("[data-testid='bank-name-input']").fill("GRE核心高频词库")
        page.locator("[data-testid='bank-note-input']").fill("2026年度官方大纲词库")
        page.locator("[data-testid='submit-bank-btn']").click()

        # 5. Wait for modal to close and card to appear
        expect(page.locator("[data-testid='bank-name-input']")).not_to_be_visible()
        page.wait_for_timeout(500)

        # Assert bank card is visible with correct text
        expect(page.get_by_text("GRE核心高频词库")).to_be_visible()

        # 6. Verify bank in SQLite database
        created_bank_id = None
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT id, name, note, archived, revision FROM bank WHERE name = 'GRE核心高频词库'")
            row = cur.fetchone()
            assert row is not None, "Bank must exist in database"
            created_bank_id, name, note, archived, revision = row
            assert name == "GRE核心高频词库"
            assert note == "2026年度官方大纲词库"
            assert archived == 0
            assert revision == 0

        # 7. Open sources drawer
        page.locator("[data-testid='manage-sources-btn']").click()
        expect(page.locator("[data-testid='source-name-input']")).to_be_visible()

        # 8. Fill source creation form
        page.locator("[data-testid='source-name-input']").fill("GRE Official 2026")
        page.locator("[data-testid='source-location-input']").fill("https://ets.org/gre/vocab")
        page.locator("[data-testid='source-version-input']").fill("v2.0")
        page.locator("[data-testid='source-license-input']").fill("Educational")
        page.locator("[data-testid='source-scope-input']").fill("gre_exam")
        page.locator("[data-testid='submit-source-btn']").click()

        # 9. Verify source card appears in drawer
        page.wait_for_timeout(500)
        expect(page.get_by_text("GRE Official 2026")).to_be_visible()

        # 10. Verify source in SQLite database
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT id, name, location, source_version, license, usage_scope FROM source WHERE name = 'GRE Official 2026'")
            srow = cur.fetchone()
            assert srow is not None, "Source must exist in database"
            _, sname, sloc, sver, slic, sscope = srow
            assert sname == "GRE Official 2026"
            assert sloc == "https://ets.org/gre/vocab"
            assert sver == "v2.0"
            assert slic == "Educational"
            assert sscope == "gre_exam"

        # 11. A failed list request must not masquerade as a genuinely empty
        # database. Preserve a visible error and a working retry path.
        page.route(
            f"{base_url}/api/v1/banks",
            lambda route: route.fulfill(
                status=500,
                content_type="application/json",
                body='{"error":{"code":"INTERNAL_ERROR","message":"simulated list failure"}}',
            ),
        )
        page.goto(f"{base_url}/banks", wait_until="networkidle")
        expect(page.locator("[data-testid='retry-load-banks-btn']")).to_be_visible()
        expect(page.locator("[data-testid='empty-banks']")).not_to_be_visible()
        page.unroute(f"{base_url}/api/v1/banks")
        page.locator("[data-testid='retry-load-banks-btn']").click()
        expect(page.get_by_text("GRE核心高频词库")).to_be_visible()

        # A successful search with no matches needs its own empty state and a
        # clear-search recovery action.
        page.locator("[data-testid='search-bank-input']").fill("no-such-bank-zzzz")
        expect(page.locator("[data-testid='no-matching-banks']")).to_be_visible()
        page.locator("[data-testid='clear-bank-search-btn']").click()
        expect(page.get_by_text("GRE核心高频词库")).to_be_visible()

        return {
            "status": "passed",
            "artifacts": harness.artifacts,
            "details": {
                "empty_state_verified": True,
                "bank_created": created_bank_id,
                "source_created": True,
                "list_error_not_misreported_as_empty": True,
                "search_no_match_state_verified": True,
            },
        }
