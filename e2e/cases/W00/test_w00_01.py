"""W00-01: 构建资源、路由、中文壳与真实资源哈希"""
import json
from pathlib import Path
import httpx
from playwright.sync_api import expect
from support.web_harness import WebHarness


def run(context: dict) -> dict:
    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page

        # 1. Access root URL
        response = page.goto(base_url, wait_until="networkidle")
        assert response.status == 200, f"Expected 200, got {response.status}"

        # 2. Check title and html lang
        title = page.title()
        assert "Adaptive Vocabulary" in title, f"Expected Adaptive Vocabulary in title, got {title}"
        lang = page.locator("html").get_attribute("lang")
        assert lang == "zh-CN", f"Expected lang='zh-CN', got {lang}"

        # 3. Check UI Shell elements
        expect(page.locator("[data-testid='app-shell']")).to_be_visible()
        expect(page.locator("[data-testid='header-logo']")).to_contain_text("Adaptive Vocabulary")
        expect(page.locator("[data-testid='nav-menu']")).to_contain_text("首页")
        expect(page.locator("[data-testid='nav-menu']")).to_contain_text("自适应练习")
        expect(page.locator("[data-testid='nav-menu']")).to_contain_text("阶段考试")
        expect(page.locator("[data-testid='nav-menu']")).to_contain_text("题库与内容")
        expect(page.locator("[data-testid='nav-menu']")).to_contain_text("共享组件")

        # 4. Check Home View rendered
        expect(page.locator("[data-testid='home-view']")).to_be_visible()
        expect(page.locator("[data-testid='card-practice']")).to_be_visible()
        expect(page.locator("[data-testid='card-exam']")).to_be_visible()
        expect(page.locator("[data-testid='card-banks']")).to_be_visible()
        expect(page.locator("[data-testid='card-components']")).to_be_visible()

        # 5. Client navigation to /components
        page.locator("[data-testid='nav-menu']").get_by_text("共享组件").click()
        page.wait_for_url(f"{base_url}/components")
        expect(page.locator("[data-testid='components-demo-view']")).to_be_visible()

        # 6. Direct navigation to /components (SPA fallback in Go server)
        reload_resp = page.goto(f"{base_url}/components", wait_until="networkidle")
        assert reload_resp.status == 200
        expect(page.locator("[data-testid='components-demo-view']")).to_be_visible()

        # 7. Check web-manifest endpoint and asset hashes
        with httpx.Client() as client:
            man_resp = client.get(f"{base_url}/api/v1/web-manifest")
            assert man_resp.status_code == 200, f"Expected 200, got {man_resp.status_code}"
            man_data = man_resp.json()
            assert man_data.get("built") is True, "Expected web frontend to be marked as built"
            assert man_data.get("asset_hash"), "Expected valid asset hash in manifest"
            manifest = man_data.get("manifest", {})
            assert "index.html" in manifest, "Expected index.html in Vite manifest"
            asset_file = manifest["index.html"].get("file")
            assert asset_file and asset_file.startswith("assets/"), f"Unexpected asset file: {asset_file}"

            # Check that Go serves the asset with Cache-Control and valid JS Content-Type
            asset_url = f"{base_url}/{asset_file}"
            asset_resp = client.get(asset_url)
            assert asset_resp.status_code == 200, f"Failed to fetch {asset_url}"
            assert "javascript" in asset_resp.headers.get("content-type", "").lower()
            assert "immutable" in asset_resp.headers.get("cache-control", "").lower()

        return {
            "status": "passed",
            "artifacts": harness.artifacts,
            "details": {
                "base_url": base_url,
                "asset_hash": man_data.get("asset_hash"),
                "network_events_count": len(harness.network_events),
            },
        }
