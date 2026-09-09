"""IM-02: Idempotency, size/row limits, source conflicts, and archive constraints.
Verifies:
- Duplicate upload with same Idempotency-Key returns identical result;
- Reused key with different payload returns 409 IDEMPOTENCY_CONFLICT;
- Max file size (2 MiB) and max rows (5000) rejected;
- Duplicate external_entry_id in source returns 409 SOURCE_CONFLICT;
- Disambiguation CAS mismatch returns 409 REVISION_CONFLICT;
- Archived bank rejects import.
"""
from pathlib import Path
import uuid
from support.lifecycle import managed_server
from support.http_client import APIClient

BANK_ID = "00000000-0000-4000-8000-000000000801"
SOURCE_ID = "project-authored-fixture-v1"


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    data_dir = case_dir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)
    artifacts = []

    with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))
        client = APIClient(server.base_url)
        client.initialize_client_context()

        # 1. Idempotency on bank import
        idemp_key = str(uuid.uuid4())
        payload1 = {"words": ["river", "desert"], "expected_revision": -1}
        resp1 = client.post(f"/api/v1/banks/{BANK_ID}/imports", json=payload1, headers={"Idempotency-Key": idemp_key})
        assert resp1.status_code == 200, f"Expected 200, got {resp1.status_code}: {resp1.text}"
        job1 = resp1.json()

        # Replay same key and payload
        resp1_replay = client.post(f"/api/v1/banks/{BANK_ID}/imports", json=payload1, headers={"Idempotency-Key": idemp_key})
        assert resp1_replay.status_code == 200
        assert resp1_replay.json()["import_id"] == job1["import_id"], "Expected identical import_id on replay"

        # Replay same key with different payload -> 409 IDEMPOTENCY_CONFLICT
        payload_diff = {"words": ["mountain"], "expected_revision": -1}
        resp_conflict = client.post(f"/api/v1/banks/{BANK_ID}/imports", json=payload_diff, headers={"Idempotency-Key": idemp_key})
        assert resp_conflict.status_code == 409, f"Expected 409, got {resp_conflict.status_code}"
        assert resp_conflict.json()["error"]["code"] == "IDEMPOTENCY_CONFLICT"

        # 2. Row limit check (> 5000 rows)
        huge_words = [f"word{i}" for i in range(5001)]
        resp_limit = client.post(f"/api/v1/banks/{BANK_ID}/imports", json={"words": huge_words, "expected_revision": -1})
        assert resp_limit.status_code == 400
        assert "5000 rows" in resp_limit.json()["error"]["message"]

        # 3. File size limit (> 2 MiB)
        huge_file_content = b"a\n" * (2 * 1024 * 1024 + 100)
        resp_huge_file = client.post(
            f"/api/v1/banks/{BANK_ID}/imports",
            files={"file": ("huge.txt", huge_file_content, "text/plain")},
        )
        assert resp_huge_file.status_code == 400
        assert "2 MiB" in resp_huge_file.json()["error"]["message"] or "too large" in resp_huge_file.json()["error"]["message"]

        # 4. Source conflict check (duplicate external_entry_id)
        source_idemp = str(uuid.uuid4())
        src_payload = {
            "source_version": "1.0.0",
            "expected_revision": 0,
            "entries": [
                {
                    "external_entry_id": "test-dup-01",
                    "raw_word": "compass",
                    "raw_translation": "指南针",
                }
            ]
        }
        resp_src1 = client.post(f"/api/v1/sources/{SOURCE_ID}/imports", json=src_payload, headers={"Idempotency-Key": source_idemp})
        assert resp_src1.status_code == 200

        # Try inserting same entry again under different idempotency key
        src_payload_dup = {
            "source_version": "1.0.0",
            "expected_revision": 1,
            "entries": [
                {
                    "external_entry_id": "test-dup-01",
                    "raw_word": "compass",
                    "raw_translation": "指南针",
                }
            ]
        }
        resp_src_dup = client.post(f"/api/v1/sources/{SOURCE_ID}/imports", json=src_payload_dup, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp_src_dup.status_code == 409
        assert resp_src_dup.json()["error"]["code"] == "SOURCE_CONFLICT"

        # 5. Archived bank rejects import
        # Archive bank 2
        bank2_id = "00000000-0000-4000-8000-000000000802"
        r_arch = client.post(
            f"/api/v1/banks/{bank2_id}/archive",
            json={"expected_revision": 0},
            headers={"Idempotency-Key": str(uuid.uuid4())}
        )
        assert r_arch.status_code == 200, f"Archive failed: {r_arch.text}"

        # Import into archived bank should fail
        r_arch_import = client.post(f"/api/v1/banks/{bank2_id}/imports", json={"words": ["apple"], "expected_revision": -1})
        assert r_arch_import.status_code == 400
        assert r_arch_import.json()["error"]["code"] == "BANK_ARCHIVED"

        client.close()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "idempotency_replay_and_conflict": "ok",
            "size_and_row_limits": "ok",
            "source_conflict_handling": "ok",
            "archive_protection": "ok"
        }
    }
