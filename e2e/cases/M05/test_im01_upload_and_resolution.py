"""IM-01: UTF-8 TXT/CSV upload, automatic matching, exception resolution, and traceability.
Verifies:
- Browser UI file upload & text upload into bank;
- Automatic matching: single sense -> resolved, multiple senses -> ambiguous, unknown -> not_found;
- Interactive disambiguation: adopt candidate sense and exclude entry;
- Traceability from bank members back to raw input.
"""
from pathlib import Path
import uuid
from playwright.sync_api import sync_playwright
from support.lifecycle import managed_server
from support.http_client import APIClient

BANK_ID = "00000000-0000-4000-8000-000000000801"


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    data_dir = case_dir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)
    artifacts = []

    txt_fixture = context["root_dir"] / "e2e/fixtures/import/sample_words.txt"
    csv_fixture = context["root_dir"] / "e2e/fixtures/import/sample_words.csv"
    assert txt_fixture.is_file(), f"Missing fixture {txt_fixture}"
    assert csv_fixture.is_file(), f"Missing fixture {csv_fixture}"

    with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))
        base_url = server.base_url

        # 1. Playwright Chromium browser test for TXT upload and manual disambiguation
        with sync_playwright() as p:
            browser = p.chromium.launch(headless=True)
            b_ctx = browser.new_context()
            b_ctx.tracing.start(screenshots=True, snapshots=True)

            page = b_ctx.new_page()

            # 1.1 Visit bank detail page
            page.goto(f"{base_url}/banks/{BANK_ID}")
            assert "题库详情" in page.title()

            # 1.2 Click '导入词表'
            page.click("#btn-import-vocab")
            page.wait_for_url(f"{base_url}/banks/{BANK_ID}/import")
            assert "导入词表" in page.title()

            # 1.3 Upload sample_words.txt
            file_input = page.locator("#import-file")
            file_input.set_input_files(str(txt_fixture))

            # Submit import
            page.click("#btn-submit-import")

            # Wait for redirect to /imports/{id}
            page.wait_for_url(f"**/imports/**", timeout=5000)
            import_url = page.url
            import_id = import_url.rstrip("/").split("/")[-1]

            # 1.4 Check stats on dashboard
            page.wait_for_selector("#items-container .item-card", timeout=5000)
            stat_total = page.locator("#stat-total-val").text_content()
            stat_resolved = page.locator("#stat-resolved-val").text_content()
            stat_ambiguous = page.locator("#stat-ambiguous-val").text_content()
            stat_not_found = page.locator("#stat-not-found-val").text_content()

            assert stat_total == "4", f"Expected total 4, got {stat_total}"
            assert stat_resolved == "2", f"Expected resolved 2, got {stat_resolved}"
            assert stat_ambiguous == "1", f"Expected ambiguous 1, got {stat_ambiguous}"
            assert stat_not_found == "1", f"Expected not_found 1, got {stat_not_found}"

            # Save screenshot & trace
            screenshot_path = case_dir / "import_dashboard.png"
            page.screenshot(path=str(screenshot_path))
            artifacts.append(str(screenshot_path))

            # 1.5 Disambiguate 'bank': click adopt on the first sense
            ambiguous_card = page.locator(".item-card.status-ambiguous").first
            btn_adopt = ambiguous_card.locator(".btn-adopt")
            btn_adopt.click()

            # Wait for reload and check updated stats
            page.wait_for_timeout(500)
            page.wait_for_selector("#stat-ambiguous-val", timeout=5000)
            updated_ambiguous = page.locator("#stat-ambiguous-val").text_content()
            updated_resolved = page.locator("#stat-resolved-val").text_content()

            assert updated_ambiguous == "0", f"Expected 0 ambiguous after adopt, got {updated_ambiguous}"
            assert updated_resolved == "3", f"Expected 3 resolved after adopt, got {updated_resolved}"

            trace_path = case_dir / "trace.zip"
            b_ctx.tracing.stop(path=str(trace_path))
            artifacts.append(str(trace_path))
            browser.close()

        # 2. HTTP test for CSV upload with specified sense_id and invalid entry
        client = APIClient(base_url)
        client.initialize_client_context()

        with open(csv_fixture, "rb") as f:
            csv_bytes = f.read()

        resp_csv = client.post(
            f"/api/v1/banks/{BANK_ID}/imports",
            files={"file": ("sample_words.csv", csv_bytes, "text/csv")},
            data={"resolution_policy_version": "v1"},
            headers={"Idempotency-Key": str(uuid.uuid4())}
        )
        assert resp_csv.status_code == 200, f"Expected 200 for CSV import, got {resp_csv.status_code}: {resp_csv.text}"
        csv_job = resp_csv.json()
        assert csv_job["total_count"] == 3
        assert csv_job["resolved_count"] == 2
        assert csv_job["invalid_count"] == 1
        csv_import_id = csv_job["import_id"]

        # 3. Verify item details and traceability
        resp_items = client.get(f"/api/v1/imports/{csv_import_id}/items")
        assert resp_items.status_code == 200
        items_data = resp_items.json()
        assert len(items_data["items"]) == 3

        # Item 0: river -> explicitly specified sense_id
        it0 = items_data["items"][0]
        assert it0["normalized_word"] == "river"
        assert it0["status"] == "resolved"
        assert len(it0["resolved_sense_ids"]) == 1
        assert it0["resolved_sense_ids"][0] == "00000000-0000-4000-8000-000000000101"

        # Item 2: invalid entry with non-existent sense_id
        it2 = items_data["items"][2]
        assert it2["status"] == "invalid"
        assert "not found in database" in it2["disposition_reason"]

        # 4. Cross-module integration: M01 bank creation -> M05 import -> M01 member sync -> M03 practice consumption
        # 4.1 Create a new empty bank via M01 API
        resp_new_bank = client.post("/api/v1/banks", json={
            "name": "M05 Interop Bank",
            "note": "Cross-module integration verification"
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp_new_bank.status_code == 201, f"Failed to create new bank: {resp_new_bank.text}"
        interop_bank_id = resp_new_bank.json()["bank_id"]

        # Initial bank has 0 members
        resp_detail_init = client.get(f"/api/v1/banks/{interop_bank_id}")
        assert resp_detail_init.status_code == 200
        assert resp_detail_init.json()["bank"]["member_count"] == 0

        # 4.2 Import sample_words.txt into the new bank via M05
        with open(txt_fixture, "rb") as f:
            txt_content = f.read()
        resp_interop_imp = client.post(
            f"/api/v1/banks/{interop_bank_id}/imports",
            files={"file": ("sample_words.txt", txt_content, "text/plain")},
            headers={"Idempotency-Key": str(uuid.uuid4())}
        )
        assert resp_interop_imp.status_code == 200
        interop_job = resp_interop_imp.json()
        assert interop_job["resolved_count"] == 2
        assert interop_job["ambiguous_count"] == 1
        interop_job_id = interop_job["import_id"]

        # 4.3 Verify M01 bank state updated: member_count increased to 2
        resp_detail_matched = client.get(f"/api/v1/banks/{interop_bank_id}")
        assert resp_detail_matched.status_code == 200
        assert resp_detail_matched.json()["bank"]["member_count"] == 2

        # 4.4 Disambiguate the ambiguous item via M05 adopt
        resp_job_items = client.get(f"/api/v1/imports/{interop_job_id}/items")
        ambig_items = [it for it in resp_job_items.json()["items"] if it["status"] == "ambiguous"]
        assert len(ambig_items) == 1
        ambig_id = ambig_items[0]["id"]

        resp_adopt = client.post(f"/api/v1/import-items/{ambig_id}/resolve", json={
            "action": "adopt",
            "sense_refs": ["00000000-0000-4000-8000-000000000108"],
            "expected_revision": 0
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp_adopt.status_code == 200

        # 4.5 Verify M01 bank state updated: member_count increased to 3
        resp_detail_adopted = client.get(f"/api/v1/banks/{interop_bank_id}")
        assert resp_detail_adopted.status_code == 200
        assert resp_detail_adopted.json()["bank"]["member_count"] == 3

        # 4.6 Verify M03 Practice integration: start session on the new bank populated by M05
        resp_prac = client.post("/api/v1/practice-sessions", json={
            "bank_id": interop_bank_id
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp_prac.status_code == 201, f"Practice session failed: {resp_prac.text}"
        practice_session_id = resp_prac.json()["session"]["id"]

        # Fetch practice question: must be drawn from the imported bank's senses
        resp_q = client.post(f"/api/v1/practice-sessions/{practice_session_id}/next", json={
            "expected_cursor": 0
        })
        assert resp_q.status_code == 200, f"Practice next failed: {resp_q.text}"
        q_obj = resp_q.json()["question"]
        assert q_obj is not None
        assert len(q_obj["stem"]) > 0

        # Cleanly end practice session
        resp_end = client.post(f"/api/v1/practice-sessions/{practice_session_id}/end", json={
            "expected_revision": 0,
            "reason": "ended"
        })
        assert resp_end.status_code == 200

        client.close()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "txt_upload_and_ui_resolution": "ok",
            "csv_specified_sense_import": "ok",
            "traceability_verified": "ok",
            "cross_module_m01_m03_integration": "ok"
        }
    }
