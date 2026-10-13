"""W04-03: 真实内容版本发现与稳定分页"""
import httpx
from playwright.sync_api import expect
from support.web_harness import WebHarness

BANK_ID = "00000000-0000-4000-8000-000000000801"


def run(context: dict) -> dict:
    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page

        # 1. Open bank detail page
        page.goto(f"{base_url}/banks/{BANK_ID}", wait_until="networkidle")
        expect(page.locator("[data-testid='bank-detail-view']")).to_be_visible()

        # 2. Click on "词义版本发现" tab
        page.get_by_role("tab", name="词义版本发现").click()
        page.wait_for_timeout(400)
        expect(page.locator("[data-testid='sense-versions-table']")).to_be_visible()
        expect(page.locator("[data-testid='sense-versions-table'] tbody tr")).not_to_have_count(0)

        # 3. Click on "题目版本发现" tab
        page.get_by_role("tab", name="题目版本发现").click()
        page.wait_for_timeout(400)
        expect(page.locator("[data-testid='question-versions-table']")).to_be_visible()
        expect(page.locator("[data-testid='question-versions-table'] tbody tr")).not_to_have_count(0)

        # 4. Anti-leak assertion: verify that question versions table does NOT leak answers or options
        table_html = page.locator("[data-testid='question-versions-table']").inner_html()
        assert "options_json" not in table_html
        assert "correct_option_id" not in table_html
        assert "explanation" not in table_html

        # 5. API contract & stable cursor pagination test for sense-versions
        client = httpx.Client(base_url=base_url)

        sense_resp1 = client.get(f"/api/v1/content/sense-versions?bank_id={BANK_ID}&limit=3")
        assert sense_resp1.status_code == 200, f"Expected 200, got {sense_resp1.status_code}"
        data1 = sense_resp1.json()
        assert "items" in data1
        assert "next_cursor" in data1
        assert "has_more" in data1
        items1 = data1["items"]
        assert len(items1) == 3, f"Expected 3 items, got {len(items1)}"
        assert data1["has_more"] is True
        cursor1 = data1["next_cursor"]
        assert cursor1 is not None and cursor1 != ""

        # Validate minimum fields of sense version
        for item in items1:
            for field in ("sense_id", "sense_version_id", "lexeme_id", "canonical_form", "definition", "status"):
                assert field in item, f"Missing field {field} in sense item"

        # Fetch page 2 with cursor
        sense_resp2 = client.get(f"/api/v1/content/sense-versions?bank_id={BANK_ID}&limit=3&cursor={cursor1}")
        assert sense_resp2.status_code == 200
        data2 = sense_resp2.json()
        items2 = data2["items"]
        assert len(items2) > 0

        # Assert no duplicate IDs between page 1 and page 2
        ids1 = {it["sense_version_id"] for it in items1}
        ids2 = {it["sense_version_id"] for it in items2}
        intersection = ids1.intersection(ids2)
        assert len(intersection) == 0, f"Pages must not have overlapping sense versions: {intersection}"

        # 6. Question version contract test
        q_resp = client.get(f"/api/v1/content/question-versions?bank_id={BANK_ID}&limit=3")
        assert q_resp.status_code == 200
        q_data = q_resp.json()
        q_items = q_data["items"]
        assert len(q_items) == 3

        for q in q_items:
            for field in ("question_version_id", "sense_id", "form_id", "task_family", "status"):
                assert field in q, f"Missing field {field} in question item"
            # Crucial: verify answers are NOT included
            assert "options" not in q
            assert "options_json" not in q
            assert "correct_option_id" not in q
            assert "explanation" not in q

        # 7. Invalid cursor returns 400 Bad Request
        bad_cursor_resp = client.get("/api/v1/content/sense-versions?cursor=!!not-valid-base64!!")
        assert bad_cursor_resp.status_code == 400
        assert bad_cursor_resp.json().get("error", {}).get("code") == "INVALID_REQUEST"

        # 8. Invalid limit returns 400 Bad Request
        bad_limit_resp = client.get("/api/v1/content/sense-versions?limit=500")
        assert bad_limit_resp.status_code == 400
        assert bad_limit_resp.json().get("error", {}).get("code") == "INVALID_REQUEST"

        return {
            "status": "passed",
            "artifacts": harness.artifacts,
            "details": {
                "sense_discovery_verified": True,
                "question_discovery_verified": True,
                "anti_leak_verified": True,
                "cursor_pagination_verified": True,
                "invalid_requests_handled": True,
            },
        }
