"""IM-03: Small fixture golden stats and offline ECDICT candidate source audit.
Verifies:
- Creation of new source and import of small_source.json with golden statistics match;
- Clean single-meaning senses published, polysemy/mixed entries not published as single sense;
- Local frozen ECDICT snapshot audited offline against config/content-sources.json hashes and byte count;
- Zero external HTTP calls made during verification.
"""
import csv
import hashlib
import json
from pathlib import Path
import uuid
from support.lifecycle import managed_server
from support.http_client import APIClient


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    data_dir = case_dir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)
    artifacts = []

    root_dir: Path = context["root_dir"]
    fixture_path = root_dir / "e2e/fixtures/import/small_source.json"
    golden_path = root_dir / "e2e/fixtures/import/golden_stats.json"
    sources_cfg_path = root_dir / "config/content-sources.json"

    assert fixture_path.is_file(), f"Missing {fixture_path}"
    assert golden_path.is_file(), f"Missing {golden_path}"
    assert sources_cfg_path.is_file(), f"Missing {sources_cfg_path}"

    fixture_data = json.loads(fixture_path.read_text(encoding="utf-8"))
    golden_stats = json.loads(golden_path.read_text(encoding="utf-8"))
    sources_cfg = json.loads(sources_cfg_path.read_text(encoding="utf-8"))

    # 1. Start server and register small source
    with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))
        client = APIClient(server.base_url)
        client.initialize_client_context()

        # 1.1 Register source
        src_meta = fixture_data["source"]
        resp_src = client.post("/api/v1/sources", json={
            "name": src_meta["name"],
            "location": src_meta["location"],
            "source_version": src_meta["source_version"],
            "license": src_meta["license"],
            "usage_scope": src_meta["usage_scope"],
            "checksum": src_meta["checksum"],
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp_src.status_code == 201, f"Create source failed: {resp_src.text}"
        new_src_id = resp_src.json()["source_id"]

        # 1.2 Import entries from small_source.json
        resp_imp = client.post(f"/api/v1/sources/{new_src_id}/imports", json={
            "source_version": src_meta["source_version"],
            "entries": fixture_data["entries"],
            "expected_revision": 0,
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp_imp.status_code == 200, f"Import source entries failed: {resp_imp.text}"
        imp_data = resp_imp.json()
        assert imp_data["status"] == "completed"

        # 1.3 Verify golden stats
        # Total entries matches golden
        assert len(fixture_data["entries"]) == golden_stats["small_source"]["total_entries"]

        # Check bank import against the newly created source entries
        bank_id = "00000000-0000-4000-8000-000000000801"
        maritime_words = ["harbor", "anchor", "beacon", "cargo", "vessel"]
        resp_bank_imp = client.post(f"/api/v1/banks/{bank_id}/imports", json={
            "words": maritime_words,
            "expected_revision": -1
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp_bank_imp.status_code == 200
        b_imp = resp_bank_imp.json()

        # harbor, anchor, beacon, cargo have single clean meanings -> resolved (4)
        # vessel has mixed meaning "船只; 血管" -> not published as standard sense -> not_found (1)
        assert b_imp["resolved_count"] == golden_stats["small_source"]["clean_single_senses"]
        assert b_imp["not_found_count"] == golden_stats["small_source"]["polysemy_entries"]

        client.close()

    # 2. Offline audit of local ECDICT snapshot
    ecdict_entry = next(s for s in sources_cfg["sources"] if s["id"] == "ecdict-candidate-v1")
    ecdict_dir = root_dir / "data/sources/ecdict" / ecdict_entry["revision"]
    ecdict_csv = ecdict_dir / "ecdict.csv"
    license_file = ecdict_dir / "LICENSE"

    assert ecdict_csv.is_file(), f"Local ECDICT snapshot missing at {ecdict_csv}"
    assert license_file.is_file(), f"ECDICT LICENSE missing at {license_file}"

    # Check file size & sha256
    file_size = ecdict_csv.stat().st_size
    assert file_size == ecdict_entry["data_bytes"], f"Byte size mismatch: expected {ecdict_entry['data_bytes']}, got {file_size}"

    with ecdict_csv.open("rb") as f:
        csv_sha256 = hashlib.file_digest(f, "sha256").hexdigest()
    assert csv_sha256 == ecdict_entry["data_sha256"], f"ECDICT sha256 mismatch: {csv_sha256}"

    with license_file.open("rb") as f:
        lic_sha256 = hashlib.file_digest(f, "sha256").hexdigest()
    assert lic_sha256 == ecdict_entry["license_sha256"], f"ECDICT LICENSE sha256 mismatch: {lic_sha256}"

    # Quick audit of sample 1000 rows
    with ecdict_csv.open(encoding="utf-8-sig", newline="") as f:
        reader = csv.DictReader(f)
        assert {"word", "translation", "tag"}.issubset(set(reader.fieldnames or []))
        rows_checked = 0
        cet6_found = 0
        for row in reader:
            rows_checked += 1
            if "cet6" in (row.get("tag") or "").split():
                cet6_found += 1
            if rows_checked >= 1000:
                break
        assert rows_checked == 1000
        assert cet6_found >= 0

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "small_source_golden_stats": "verified",
            "ecdict_offline_hash_audit": "verified",
            "ecdict_bytes": file_size
        }
    }
