"""W05-01: 输入格式与大小/行数边界"""
from pathlib import Path
import httpx
from playwright.sync_api import expect
from support.web_harness import WebHarness

FIXTURES_DIR = Path(__file__).resolve().parents[2] / "fixtures" / "web" / "imports"


def run(context: dict) -> dict:
    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page

        # 1. Open imports page
        page.goto(f"{base_url}/imports", wait_until="networkidle")
        expect(page.locator("[data-testid='imports-view']")).to_be_visible()
        expect(page.locator("[data-testid='import-create-card']")).to_be_visible()

        # -------------------------------------------------------------
        # 2. Test Format 1: Text Paste (TXT format)
        # -------------------------------------------------------------
        page.locator("[data-testid='import-text-input']").fill("river\nmountain\n")
        page.locator("[data-testid='submit-import-btn']").click()

        # Wait for summary card
        expect(page.locator("[data-testid='import-summary-card']")).to_be_visible()
        expect(page.locator("[data-testid='stat-total']")).to_contain_text("2")
        expect(page.locator("[data-testid='stat-resolved']")).to_contain_text("2")

        # Click new import to return to upload form
        page.locator("[data-testid='new-import-btn']").click()
        expect(page.locator("[data-testid='import-create-card']")).to_be_visible()

        # -------------------------------------------------------------
        # 3. Test Format 2: CSV File upload
        # -------------------------------------------------------------
        page.locator(".ant-tabs-tab[data-node-key='file']").click()
        page.wait_for_timeout(300)
        csv_path = str(FIXTURES_DIR / "sample_words.csv")
        page.locator("[data-testid='import-file-upload'] input[type='file']").set_input_files(csv_path)

        page.locator("[data-testid='submit-import-btn']").click()
        expect(page.locator("[data-testid='import-summary-card']")).to_be_visible()
        expect(page.locator("[data-testid='stat-total']")).to_contain_text("3")

        page.locator("[data-testid='new-import-btn']").click()
        expect(page.locator("[data-testid='import-create-card']")).to_be_visible()

        # -------------------------------------------------------------
        # 4. Test Format 3: JSON File upload
        # -------------------------------------------------------------
        page.locator(".ant-tabs-tab[data-node-key='file']").click()
        page.wait_for_timeout(300)
        json_path = str(FIXTURES_DIR / "sample_words.json")
        page.locator("[data-testid='import-file-upload'] input[type='file']").set_input_files(json_path)

        page.locator("[data-testid='submit-import-btn']").click()
        expect(page.locator("[data-testid='import-summary-card']")).to_be_visible()
        expect(page.locator("[data-testid='stat-total']")).to_contain_text("3")

        page.locator("[data-testid='new-import-btn']").click()
        expect(page.locator("[data-testid='import-create-card']")).to_be_visible()

        # -------------------------------------------------------------
        # 5. Boundary Test 1: >5000 rows validation
        # -------------------------------------------------------------
        # Switch back to text mode tab
        page.locator(".ant-tabs-tab[data-node-key='text']").click()
        page.wait_for_timeout(300)
        big_text = "\n".join([f"testword{i}" for i in range(5002)])
        page.locator("[data-testid='import-text-input']").fill(big_text)
        expect(page.locator("[data-testid='row-count-error']")).to_be_visible()
        expect(page.locator("[data-testid='submit-import-btn']")).to_be_disabled()

        # Backend boundary check via HTTP
        client = httpx.Client(base_url=base_url)
        csrf_resp = client.get("/client-context")
        csrf_token = csrf_resp.json().get("csrf_token", "")

        # Get first bank ID
        banks_resp = client.get("/api/v1/banks")
        first_bank = banks_resp.json()["items"][0]
        bank_id = first_bank["id"]

        overlimit_file_path = FIXTURES_DIR / "overlimit_5001.txt"
        with open(overlimit_file_path, "rb") as f:
            resp_5001 = client.post(
                f"/api/v1/banks/{bank_id}/imports",
                headers={"X-CSRF-Token": csrf_token},
                files={"file": ("overlimit.txt", f, "text/plain")},
                data={"expected_revision": first_bank["revision"]},
            )
        assert resp_5001.status_code == 400, f"Expected 400 for >5000 rows, got {resp_5001.status_code}"
        assert "5000" in resp_5001.text

        # -------------------------------------------------------------
        # 6. Boundary Test 2: >2 MiB file size validation
        # -------------------------------------------------------------
        page.locator("[data-testid='import-text-input']").fill("")  # Clear text
        page.locator(".ant-tabs-tab[data-node-key='file']").click()
        page.wait_for_timeout(300)
        overlimit_2mb_path = str(FIXTURES_DIR / "overlimit_2mb.txt")
        page.locator("[data-testid='import-file-upload'] input[type='file']").set_input_files(overlimit_2mb_path)

        expect(page.locator("[data-testid='file-size-error']")).to_be_visible()
        expect(page.locator("[data-testid='submit-import-btn']")).to_be_disabled()

        # Backend boundary check via HTTP
        with open(overlimit_2mb_path, "rb") as f:
            resp_2mb = client.post(
                f"/api/v1/banks/{bank_id}/imports",
                headers={"X-CSRF-Token": csrf_token},
                files={"file": ("overlimit.txt", f, "text/plain")},
                data={"expected_revision": first_bank["revision"]},
            )
        assert resp_2mb.status_code == 400, f"Expected 400 for >2 MiB file, got {resp_2mb.status_code}"

        return {
            "status": "passed",
            "artifacts": harness.artifacts,
            "details": {
                "txt_import_verified": True,
                "csv_import_verified": True,
                "json_import_verified": True,
                "row_limit_boundary_verified": True,
                "file_size_boundary_verified": True,
            },
        }
