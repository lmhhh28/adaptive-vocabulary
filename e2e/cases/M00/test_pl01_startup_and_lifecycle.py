"""PL-01: Startup and lifecycle acceptance test.
Verifies fresh startup -> browser UI check & health check -> restart -> versions consistency.
"""
from pathlib import Path
from playwright.sync_api import sync_playwright
from support.lifecycle import managed_server
from support.http_client import APIClient


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    data_dir = case_dir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)

    artifacts = []

    # 1. Start fresh server
    with managed_server(bin_path, data_dir, listen_port=0, log_dir=case_dir) as server1:
        base_url = server1.base_url
        artifacts.append(str(server1.log_path))

        # 2. Browser check using Playwright Chromium
        with sync_playwright() as p:
            browser = p.chromium.launch(headless=True)
            b_ctx = browser.new_context()
            b_ctx.tracing.start(screenshots=True, snapshots=True)

            page = b_ctx.new_page()
            page.goto(base_url)

            # Assert UI content
            assert "Adaptive Vocabulary" in page.title(), f"Unexpected page title: {page.title()}"
            title_text = page.locator("#app-title").text_content()
            assert title_text == "Adaptive Vocabulary", f"Unexpected title text: {title_text}"

            contract_ver = page.locator("#contract-version").text_content()
            assert contract_ver == "1.1.0", f"Unexpected contract version: {contract_ver}"

            schema_ver = page.locator("#schema-version").text_content()
            assert int(schema_ver) >= 0, f"Unexpected schema version: {schema_ver}"

            sqlite_ver = page.locator("#sqlite-version").text_content()
            assert sqlite_ver and len(sqlite_ver.strip()) > 0, "SQLite version is missing"

            # Save screenshot & trace
            screenshot_path = case_dir / "browser.png"
            page.screenshot(path=str(screenshot_path))
            artifacts.append(str(screenshot_path))

            trace_path = case_dir / "trace.zip"
            b_ctx.tracing.stop(path=str(trace_path))
            artifacts.append(str(trace_path))

            browser.close()

        # 3. HTTP health check
        client = APIClient(base_url)
        resp = client.get("/api/v1/health")
        assert resp.status_code == 200, f"Health check returned status {resp.status_code}"
        health = resp.json()
        assert health["status"] == "ok"
        assert health["schema_version"] >= 0
        assert health["contract_version"] == "1.1.0"
        assert health["mode"] == "active"
        client.close()

    # 4. Restart server on the same data directory
    with managed_server(bin_path, data_dir, listen_port=0, log_dir=case_dir) as server2:
        client2 = APIClient(server2.base_url)

        resp2 = client2.get("/api/v1/health")
        assert resp2.status_code == 200, f"Health check on restart returned {resp2.status_code}"
        health2 = resp2.json()
        assert health2["status"] == "ok"
        assert health2["schema_version"] >= 0
        assert health2["contract_version"] == "1.1.0"
        assert health2["mode"] == "active"

        resp_maint = client2.get("/api/v1/maintenance")
        assert resp_maint.status_code == 200
        maint = resp_maint.json()
        assert maint["mode"] == "active"
        assert maint["active_version"] == "v1"
        assert maint["requires_rebuild"] is False

        client2.close()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "schema_version": int(schema_ver),
            "contract_version": "1.1.0",
            "mode": "active",
        },
    }
