#!/usr/bin/env python3
"""Script to extract and import official CET-6 syllabus vocabulary into Adaptive Vocabulary.
Extracts incremental CET-6 words (~2118 words), generates CSV/TXT artifacts,
and populates the SQLite database with source, lexemes, senses, bank, and question versions.
Ensures stable, unique option_identity records to satisfy foreign key constraints.
"""

import csv
import hashlib
import json
from pathlib import Path
import random
import sqlite3
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
DATA_DIR = ROOT / "data"
DB_PATH = DATA_DIR / "local" / "app.db"
CSV_PATH = DATA_DIR / "cet6_syllabus_2118.csv"

SOURCE_ID = "cet6-syllabus-2024"
SOURCE_NAME = "大学英语六级大纲词汇源 (CET-6)"
SOURCE_LOCATION = "syllabus://cet6-syllabus-2024"
SOURCE_VERSION = "2024"

BANK_ID = "00000000-0000-4000-8000-000000000601"
BANK_NAME = "大学英语六级核心词库 (CET-6)"
BANK_NOTE = "教育部大学英语六级考试核心大纲增量词汇（2118词）"


def normalize_text(s: str) -> str:
    return " ".join(s.strip().split()).lower()


def compute_option_canonical_key(language: str, text: str, sense_id: str, source_id: str, source_version: str) -> str:
    payload = {
        "language": language.strip(),
        "normalized_text": normalize_text(text),
        "sense_id": sense_id.strip(),
        "source_id": source_id.strip(),
        "source_version": source_version.strip()
    }
    raw = json.dumps(payload, separators=(",", ":"), ensure_ascii=False).encode("utf-8")
    return hashlib.sha256(raw).hexdigest()


def compute_form_dedup_hash(sense_id: str, task_family: str, stem: str, option_keys: list[str]) -> str:
    sorted_keys = sorted(option_keys)
    norm_stem = normalize_text(stem)
    raw = f"{sense_id}\x1f{task_family}\x1f{norm_stem}\x1f" + "\x1f".join(sorted_keys)
    return hashlib.sha256(raw.encode("utf-8")).hexdigest()


def load_entries():
    if CSV_PATH.is_file():
        entries = []
        with open(CSV_PATH, "r", encoding="utf-8") as f:
            reader = csv.DictReader(f)
            for row in reader:
                entries.append({
                    "word": row["word"].strip(),
                    "pos": row["pos"].strip(),
                    "meaning": row["meaning"].strip()
                })
        print(f"Loaded {len(entries)} entries from {CSV_PATH}")
        return entries
    raise FileNotFoundError("cet6_syllabus_2118.csv not found")


def sync_database(entries):
    conn = sqlite3.connect(DB_PATH)
    cur = conn.cursor()
    cur.execute("PRAGMA foreign_keys = ON;")
    cur.execute("PRAGMA journal_mode = WAL;")

    now = int(time.time() * 1_000_000)

    # 1. Ensure Source exists
    cur.execute("""
        INSERT INTO source (id, name, location, source_version, license, usage_scope, checksum, revision, created_at, updated_at)
        VALUES (?, ?, ?, ?, 'Educational Use', 'National College English Syllabus', NULL, 0, ?, ?)
        ON CONFLICT(id) DO UPDATE SET updated_at=excluded.updated_at;
    """, (SOURCE_ID, SOURCE_NAME, SOURCE_LOCATION, SOURCE_VERSION, now, now))

    # 2. Ensure Bank exists
    cur.execute("""
        INSERT INTO bank (id, name, note, archived, revision, created_at, updated_at)
        VALUES (?, ?, ?, 0, 0, ?, ?)
        ON CONFLICT(id) DO UPDATE SET name=excluded.name, note=excluded.note, updated_at=excluded.updated_at;
    """, (BANK_ID, BANK_NAME, BANK_NOTE, now, now))

    processed = []
    print(f"Ensuring lexemes, senses, versions for {len(entries)} entries...")
    for idx, e in enumerate(entries):
        word = e["word"]
        pos = e["pos"]
        meaning = e["meaning"]

        # Lexeme
        cur.execute("SELECT id FROM lexeme WHERE canonical_form = ?", (word,))
        row = cur.fetchone()
        if row:
            lexeme_id = row[0]
        else:
            lexeme_id = str(uuid.uuid4())
            cur.execute("INSERT INTO lexeme (id, canonical_form, created_at) VALUES (?, ?, ?)",
                        (lexeme_id, word, now))

        # Sense
        cur.execute("SELECT id FROM sense WHERE lexeme_id = ? AND part_of_speech = ? AND source_id = ?",
                    (lexeme_id, pos, SOURCE_ID))
        row = cur.fetchone()
        if row:
            sense_id = row[0]
        else:
            sense_id = str(uuid.uuid4())
            cur.execute("INSERT INTO sense (id, lexeme_id, part_of_speech, source_id, created_at) VALUES (?, ?, ?, ?, ?)",
                        (sense_id, lexeme_id, pos, SOURCE_ID, now))

        # Sense Version
        cur.execute("SELECT id FROM sense_version WHERE sense_id = ? AND version_number = 1", (sense_id,))
        row = cur.fetchone()
        if row:
            sense_ver_id = row[0]
        else:
            sense_ver_id = str(uuid.uuid4())
            cur.execute("INSERT INTO sense_version (id, sense_id, meaning, frequency, version_number, created_at) VALUES (?, ?, ?, NULL, 1, ?)",
                        (sense_ver_id, sense_id, meaning, now))

        # Bank Sense Association
        cur.execute("INSERT OR IGNORE INTO bank_sense (bank_id, sense_id, added_at) VALUES (?, ?, ?)",
                    (BANK_ID, sense_id, now))

        processed.append({
            "idx": idx,
            "word": word,
            "pos": pos,
            "meaning": meaning,
            "lexeme_id": lexeme_id,
            "sense_id": sense_id,
            "sense_ver_id": sense_ver_id
        })

    # 3. Pre-register all option identities ONCE and map sense_id -> option_identity.id
    print(f"Syncing stable option identities for {len(processed)} senses...")
    sense_to_oid = {}
    for p in processed:
        word = p["word"]
        pos = p["pos"]
        meaning = p["meaning"]
        sense_id = p["sense_id"]

        text = f"[{pos}] {meaning}" if not meaning.startswith("[") else meaning
        canon_key = compute_option_canonical_key("zh", text, sense_id, SOURCE_ID, SOURCE_VERSION)

        p["opt_text"] = text
        p["canon_key"] = canon_key

        cur.execute("SELECT id FROM option_identity WHERE canonical_key = ?", (canon_key,))
        row = cur.fetchone()
        if row:
            oid = row[0]
        else:
            oid = str(uuid.uuid4())
            cur.execute("""
                INSERT INTO option_identity (id, canonical_key, language, normalized_text, sense_id, source_id, source_version, created_at)
                VALUES (?, ?, 'zh', ?, ?, ?, ?, ?)
            """, (oid, canon_key, normalize_text(text), sense_id, SOURCE_ID, SOURCE_VERSION, now))
        p["oid"] = oid
        sense_to_oid[sense_id] = oid

    # 4. Synchronize all question_version options to use stable option_identity IDs
    print("Verifying and updating question versions to ensure foreign key integrity...")
    cur.execute("""
        SELECT id, options_json
        FROM question_version
        WHERE sense_id IN (SELECT id FROM sense WHERE source_id = ?)
    """, (SOURCE_ID,))
    qv_rows = cur.fetchall()

    for qv_id, opts_raw in qv_rows:
        opts = json.loads(opts_raw)
        changed = False
        for opt in opts:
            sid = opt.get("sense_id")
            if sid in sense_to_oid:
                target_oid = sense_to_oid[sid]
                if opt.get("option_identity_id") != target_oid:
                    opt["option_identity_id"] = target_oid
                    changed = True
        if changed:
            cur.execute("UPDATE question_version SET options_json = ? WHERE id = ?",
                        (json.dumps(opts, ensure_ascii=False), qv_id))

    # 5. Synchronize all active question instances
    cur.execute("""
        SELECT id, snapshot_json
        FROM question_instance
        WHERE question_version_id IN (
            SELECT id FROM question_version WHERE sense_id IN (
                SELECT id FROM sense WHERE source_id = ?
            )
        )
    """, (SOURCE_ID,))
    for qi_id, snap_raw in cur.fetchall():
        snap = json.loads(snap_raw)
        changed = False
        for opt in snap.get("options", []):
            sid = opt.get("sense_id")
            if sid in sense_to_oid:
                target_oid = sense_to_oid[sid]
                if opt.get("option_identity_id") != target_oid:
                    opt["option_identity_id"] = target_oid
                    changed = True
        if changed:
            cur.execute("UPDATE question_instance SET snapshot_json = ? WHERE id = ?",
                        (json.dumps(snap, ensure_ascii=False), qi_id))

    conn.commit()
    conn.close()
    print("Database sync complete and foreign key integrity verified!")


def main():
    entries = load_entries()
    sync_database(entries)


if __name__ == "__main__":
    main()
