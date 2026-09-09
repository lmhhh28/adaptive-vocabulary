"""CT-02: Cross-bank references, member removal, archiving, and lifecycle protections.
Verifies cross-bank shared sense references -> member removal non-cascading -> bank archive ->
revision CAS & idempotency replay/conflict -> active session blocking.
"""
from pathlib import Path
import sqlite3
import uuid
from playwright.sync_api import sync_playwright
from support.lifecycle import managed_server
from support.http_client import APIClient


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    data_dir = case_dir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)
    artifacts = []

    with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        base_url = server.base_url
        artifacts.append(str(server.log_path))

        client = APIClient(base_url)
        csrf_token = client.initialize_client_context()

        # 1. Inspect banks and shared sense
        resp_banks = client.get("/api/v1/banks")
        assert resp_banks.status_code == 200
        banks = resp_banks.json()["items"]
        bank1 = next(b for b in banks if b["name"] == "开发样例")
        bank2 = next(b for b in banks if b["name"] == "跨库共享样例")

        # River sense id
        river_sense_id = "00000000-0000-4000-8000-000000000101"

        # Check Bank 2 detail
        resp_b2 = client.get(f"/api/v1/banks/{bank2['id']}")
        assert resp_b2.status_code == 200
        b2_detail = resp_b2.json()["bank"]
        b2_senses = {m["sense_id"] for m in b2_detail["members"]}
        assert river_sense_id in b2_senses, "river sense not found in Bank 2"

        # Browser verification on Bank 2 detail page
        with sync_playwright() as p:
            browser = p.chromium.launch(headless=True)
            b_ctx = browser.new_context()
            page = b_ctx.new_page()
            page.goto(f"{base_url}/banks/{bank2['id']}")
            assert "跨库共享样例" in page.title()
            assert page.locator("#bank-detail-name").text_content().strip() == "跨库共享样例"
            assert page.locator("#member-count-val").text_content().strip() == "3"

            screenshot_path = case_dir / "bank2_detail_browser.png"
            page.screenshot(path=str(screenshot_path))
            artifacts.append(str(screenshot_path))
            browser.close()

        # Check Bank 1 detail
        resp_b1 = client.get(f"/api/v1/banks/{bank1['id']}")
        assert resp_b1.status_code == 200
        b1_detail = resp_b1.json()["bank"]
        b1_senses = {m["sense_id"] for m in b1_detail["members"]}
        assert river_sense_id in b1_senses, "river sense not found in Bank 1"

        # 2. Test Member Removal on Bank 2
        # Check A: Missing expected_revision -> 428 PRECONDITION_REQUIRED
        idem_key1 = str(uuid.uuid4())
        resp_no_rev = client.post(
            f"/api/v1/banks/{bank2['id']}/members/remove",
            json={"sense_ids": [river_sense_id]},
            headers={"Idempotency-Key": idem_key1},
        )
        assert resp_no_rev.status_code == 428, f"Expected 428 for missing expected_revision, got {resp_no_rev.status_code}"
        assert resp_no_rev.json()["error"]["code"] == "PRECONDITION_REQUIRED"

        # Check B: Mismatched expected_revision -> 409 REVISION_CONFLICT
        idem_key2 = str(uuid.uuid4())
        resp_bad_rev = client.post(
            f"/api/v1/banks/{bank2['id']}/members/remove",
            json={"sense_ids": [river_sense_id], "expected_revision": 999},
            headers={"Idempotency-Key": idem_key2},
        )
        assert resp_bad_rev.status_code == 409, f"Expected 409 for bad revision, got {resp_bad_rev.status_code}"
        assert resp_bad_rev.json()["error"]["code"] == "REVISION_CONFLICT"

        # Check C: Valid removal of river sense from Bank 2 (revision 0 -> 1)
        idem_key3 = str(uuid.uuid4())
        payload = {"sense_ids": [river_sense_id], "expected_revision": 0}
        resp_remove = client.post(
            f"/api/v1/banks/{bank2['id']}/members/remove",
            json=payload,
            headers={"Idempotency-Key": idem_key3},
        )
        assert resp_remove.status_code == 200, f"Expected 200, got {resp_remove.status_code}: {resp_remove.text}"
        data_remove = resp_remove.json()
        assert data_remove["removed_count"] == 1
        assert data_remove["revision"] == 1

        # Check D: Idempotency replay with identical payload -> 200 with Idempotent-Replayed: true
        resp_replay = client.post(
            f"/api/v1/banks/{bank2['id']}/members/remove",
            json=payload,
            headers={"Idempotency-Key": idem_key3},
        )
        assert resp_replay.status_code == 200
        assert resp_replay.headers.get("Idempotent-Replayed") == "true"
        assert resp_replay.json()["revision"] == 1

        # Check E: Idempotency conflict with same key but different payload -> 409 IDEMPOTENCY_CONFLICT
        resp_conflict = client.post(
            f"/api/v1/banks/{bank2['id']}/members/remove",
            json={"sense_ids": ["00000000-0000-4000-8000-000000000108"], "expected_revision": 1},
            headers={"Idempotency-Key": idem_key3},
        )
        assert resp_conflict.status_code == 409
        assert resp_conflict.json()["error"]["code"] == "IDEMPOTENCY_CONFLICT"

        # Check F: Non-cascading verification: Bank 1 and global senses untouched
        resp_b1_after = client.get(f"/api/v1/banks/{bank1['id']}")
        assert resp_b1_after.status_code == 200
        b1_members_after = {m["sense_id"] for m in resp_b1_after.json()["bank"]["members"]}
        assert river_sense_id in b1_members_after, "river was incorrectly removed from Bank 1!"

        db_file = data_dir / "app.db"
        with sqlite3.connect(str(db_file), timeout=5.0) as conn:
            cur = conn.cursor()
            cur.execute("SELECT COUNT(*) FROM sense WHERE id = ?", (river_sense_id,))
            assert cur.fetchone()[0] == 1, "Global sense was deleted from database!"
            cur.execute("SELECT COUNT(*) FROM question_version WHERE sense_id = ?", (river_sense_id,))
            assert cur.fetchone()[0] > 0, "Question versions for sense were deleted!"

        # 3. Bank Archiving on Bank 2 (revision 1 -> 2)
        idem_archive = str(uuid.uuid4())
        resp_arch = client.post(
            f"/api/v1/banks/{bank2['id']}/archive",
            json={"expected_revision": 1},
            headers={"Idempotency-Key": idem_archive},
        )
        assert resp_arch.status_code == 200, f"Expected 200 for archive, got {resp_arch.status_code}: {resp_arch.text}"
        arch_data = resp_arch.json()
        assert arch_data["archived"] is True
        assert arch_data["revision"] == 2

        # Check Bank 2 still has its remaining 2 members and create active session
        with sqlite3.connect(str(db_file), timeout=5.0) as conn:
            cur = conn.cursor()
            cur.execute("SELECT COUNT(*) FROM bank_sense WHERE bank_id = ?", (bank2["id"],))
            assert cur.fetchone()[0] == 2, "Archived bank lost its members!"

            # 4. Active Session Protection (HTTP 409 SESSION_ACTIVE)
            cur.execute("""
                INSERT INTO session (id, kind, status, bank_id, cursor, normal_position, revision, last_ack_sequence, created_at, updated_at)
                VALUES (?, 'practice', 'open', ?, 0, 1, 0, 0, 1000, 1000)
            """, (str(uuid.uuid4()), bank1["id"]))
            conn.commit()

        # Reading bank detail must be blocked during active session
        resp_detail_blocked = client.get(f"/api/v1/banks/{bank1['id']}")
        assert resp_detail_blocked.status_code == 409, f"Expected 409 SESSION_ACTIVE, got {resp_detail_blocked.status_code}"
        assert resp_detail_blocked.json()["error"]["code"] == "SESSION_ACTIVE"

        # Member removal must be blocked
        resp_remove_blocked = client.post(
            f"/api/v1/banks/{bank1['id']}/members/remove",
            json={"sense_ids": [river_sense_id], "expected_revision": 0},
            headers={"Idempotency-Key": str(uuid.uuid4())},
        )
        assert resp_remove_blocked.status_code == 409
        assert resp_remove_blocked.json()["error"]["code"] == "SESSION_ACTIVE"

        # Archive must be blocked
        resp_arch_blocked = client.post(
            f"/api/v1/banks/{bank1['id']}/archive",
            json={"expected_revision": 0},
            headers={"Idempotency-Key": str(uuid.uuid4())},
        )
        assert resp_arch_blocked.status_code == 409
        assert resp_arch_blocked.json()["error"]["code"] == "SESSION_ACTIVE"

        # Close session
        with sqlite3.connect(str(db_file), timeout=5.0) as conn:
            conn.execute("UPDATE session SET status = 'ended' WHERE status = 'open'")
            conn.commit()

        # Reading bank detail succeeds again
        resp_detail_ok = client.get(f"/api/v1/banks/{bank1['id']}")
        assert resp_detail_ok.status_code == 200

        client.close()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "cross_bank_sharing": "verified",
            "non_cascading_removal": "verified",
            "archive": "verified",
            "idempotency_replay": "verified",
            "active_session_blocking": "verified",
        },
    }
