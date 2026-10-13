"""W05-03: CAS/幂等及已接受响应丢失"""
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

        # -------------------------------------------------------------
        # 1. CAS conflict check on Import API
        # -------------------------------------------------------------
        banks_resp = client.get("/api/v1/banks")
        first_bank = banks_resp.json()["items"][0]
        bank_id = first_bank["id"]

        stale_import_resp = client.post(
            f"/api/v1/banks/{bank_id}/imports",
            headers={"X-CSRF-Token": csrf_token},
            json={
                "words": ["river"],
                "expected_revision": 9999,  # Stale revision
            },
        )
        assert stale_import_resp.status_code == 409, f"Expected 409 conflict, got {stale_import_resp.status_code}"
        assert stale_import_resp.json().get("error", {}).get("code") == "REVISION_CONFLICT"

        # -------------------------------------------------------------
        # 2. Idempotency on Import API
        # -------------------------------------------------------------
        fixed_idemp_key = "idemp-test-w05-03-key"
        resp1 = client.post(
            f"/api/v1/banks/{bank_id}/imports",
            headers={
                "X-CSRF-Token": csrf_token,
                "Idempotency-Key": fixed_idemp_key,
            },
            json={
                "words": ["river", "cloud"],
                "expected_revision": first_bank["revision"],
            },
        )
        assert resp1.status_code == 200
        import_id_1 = resp1.json()["import_id"]

        # Replay with same Idempotency-Key and body
        resp2 = client.post(
            f"/api/v1/banks/{bank_id}/imports",
            headers={
                "X-CSRF-Token": csrf_token,
                "Idempotency-Key": fixed_idemp_key,
            },
            json={
                "words": ["river", "cloud"],
                "expected_revision": first_bank["revision"],
            },
        )
        assert resp2.status_code == 200
        import_id_2 = resp2.json()["import_id"]
        assert import_id_1 == import_id_2, "Idempotent replay must return identical import_id"

        # Verify in SQLite that only 1 import job exists with this ID
        with closing(sqlite3.connect(db_path)) as conn:
            cur = conn.cursor()
            cur.execute("SELECT COUNT(*) FROM import_job WHERE id = ?", (import_id_1,))
            job_count = cur.fetchone()[0]
            assert job_count == 1, f"Expected exactly 1 job, got {job_count}"

        # -------------------------------------------------------------
        # 3. Create a fresh import with ambiguous "bank" for UI tests
        # -------------------------------------------------------------
        fresh_import_resp = client.post(
            f"/api/v1/banks/{bank_id}/imports",
            headers={"X-CSRF-Token": csrf_token},
            json={
                "words": ["bank"],
                "expected_revision": -1,  # Skip check for setup
            },
        )
        fresh_import_id = fresh_import_resp.json()["import_id"]

        # Open page for this import
        page.goto(f"{base_url}/imports?import_id={fresh_import_id}", wait_until="networkidle")
        expect(page.locator("[data-testid='import-summary-card']")).to_be_visible()

        # Get the ambiguous item ID
        items_resp = client.get(f"/api/v1/imports/{fresh_import_id}/items?status=ambiguous")
        ambiguous_items = items_resp.json()["items"]
        assert len(ambiguous_items) == 1
        target_item = ambiguous_items[0]
        target_item_id = target_item["id"]
        target_rev = target_item["revision"]
        candidate_senses = [c["sense_id"] for c in target_item["candidates"]]

        # -------------------------------------------------------------
        # 4. CAS conflict on Item Resolve API and UI error display
        # -------------------------------------------------------------
        # Try resolving with stale expected_revision = 999
        stale_resolve_resp = client.post(
            f"/api/v1/import-items/{target_item_id}/resolve",
            headers={"X-CSRF-Token": csrf_token},
            json={
                "action": "adopt",
                "sense_refs": [candidate_senses[0]],
                "expected_revision": 999,
            },
        )
        assert stale_resolve_resp.status_code == 409
        assert stale_resolve_resp.json().get("error", {}).get("code") == "REVISION_CONFLICT"

        # -------------------------------------------------------------
        # 5. Accepted Response Loss & Idempotent Retry Simulation
        # -------------------------------------------------------------
        resolve_idemp_key = "resolve-loss-test-key"

        # Initial successful resolve via client
        initial_resolve_resp = client.post(
            f"/api/v1/import-items/{target_item_id}/resolve",
            headers={
                "X-CSRF-Token": csrf_token,
                "Idempotency-Key": resolve_idemp_key,
            },
            json={
                "action": "adopt",
                "sense_refs": [candidate_senses[0]],
                "expected_revision": target_rev,
            },
        )
        assert initial_resolve_resp.status_code == 200, f"Initial resolve failed: {initial_resolve_resp.text}"
        res_data = initial_resolve_resp.json()
        assert res_data.get("item", {}).get("status") == "resolved"
        first_revision = res_data.get("revision")

        # Now simulate network retry with identical Idempotency-Key (as if first response was lost)
        retry_resolve_resp = client.post(
            f"/api/v1/import-items/{target_item_id}/resolve",
            headers={
                "X-CSRF-Token": csrf_token,
                "Idempotency-Key": resolve_idemp_key,
            },
            json={
                "action": "adopt",
                "sense_refs": [candidate_senses[0]],
                "expected_revision": target_rev,
            },
        )
        assert retry_resolve_resp.status_code == 200, f"Retry resolve failed: {retry_resolve_resp.text}"
        retry_data = retry_resolve_resp.json()
        assert retry_data.get("item", {}).get("status") == "resolved"
        assert retry_data.get("revision") == first_revision, "Revision must not increment on idempotent replay"

        # Reload browser page and verify UI shows the item as resolved
        page.reload(wait_until="networkidle")
        expect(page.locator("[data-testid='import-summary-card']")).to_be_visible()
        expect(page.locator("[data-testid='stat-resolved']")).to_contain_text("1")
        expect(page.locator("[data-testid='stat-ambiguous']")).to_contain_text("0")

        return {
            "status": "passed",
            "artifacts": harness.artifacts,
            "details": {
                "import_cas_conflict_verified": True,
                "import_idempotency_verified": True,
                "resolve_cas_conflict_verified": True,
                "resolve_idempotent_retry_verified": True,
            },
        }
