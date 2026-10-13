#!/usr/bin/env python3
"""Split CET-6 syllabus vocabulary into daily 80-word banks (Days 01-27).
Creates individual daily banks in SQLite, associates bank senses, and exports daily CSV/TXT files.
"""

import csv
from pathlib import Path
import sqlite3
import time

ROOT = Path(__file__).resolve().parents[1]
DATA_DIR = ROOT / "data"
DAILY_DIR = DATA_DIR / "daily"
CSV_PATH = DATA_DIR / "cet6_syllabus_2118.csv"
DB_PATH = DATA_DIR / "local" / "app.db"
SOURCE_ID = "cet6-syllabus-2024"
CHUNK_SIZE = 80


def main():
    if not CSV_PATH.is_file():
        raise FileNotFoundError(f"{CSV_PATH} not found")

    DAILY_DIR.mkdir(parents=True, exist_ok=True)

    entries = []
    with open(CSV_PATH, "r", encoding="utf-8") as f:
        reader = csv.DictReader(f)
        for row in reader:
            entries.append({
                "word": row["word"].strip(),
                "pos": row["pos"].strip(),
                "meaning": row["meaning"].strip()
            })

    total_words = len(entries)
    print(f"Loaded {total_words} words from {CSV_PATH}")

    conn = sqlite3.connect(DB_PATH)
    cur = conn.cursor()
    cur.execute("PRAGMA foreign_keys = ON;")
    cur.execute("PRAGMA journal_mode = WAL;")

    # 1. Fetch mapping from word -> sense_id for CET-6 source
    cur.execute("""
        SELECT l.canonical_form, s.id
        FROM sense s
        JOIN lexeme l ON l.id = s.lexeme_id
        WHERE s.source_id = ?
    """, (SOURCE_ID,))
    word_to_sense = {}
    for word, sid in cur.fetchall():
        word_to_sense[word] = sid

    print(f"Found {len(word_to_sense)} sense mappings in database for {SOURCE_ID}")

    # 2. Update master bank name
    cur.execute("""
        UPDATE bank
        SET name = '大学英语六级 · 全量总库 (2118词)',
            note = '教育部大学英语六级考试核心大纲增量全量词库（共2118词，涵盖Day 01至Day 27全部内容）'
        WHERE id = '00000000-0000-4000-8000-000000000601'
    """)

    # 3. Split into daily chunks
    base_time = int(time.time() * 1_000_000)
    day_idx = 1
    created_banks = []

    for start_i in range(0, total_words, CHUNK_SIZE):
        chunk = entries[start_i:start_i + CHUNK_SIZE]
        first_word = chunk[0]["word"]
        last_word = chunk[-1]["word"]
        count = len(chunk)

        bank_uuid = f"00000000-0000-4000-8000-00000006{day_idx:04d}"
        bank_name = f"大学英语六级 · 第{day_idx:02d}天 ({count}词)"
        bank_note = f"六级大纲每日背词计划 · 第 {day_idx:02d} 天（范围: {first_word} ~ {last_word}，共 {count} 词）"
        created_at = base_time + day_idx * 1000  # monotonically increasing

        # Insert or update bank
        cur.execute("""
            INSERT INTO bank (id, name, note, archived, revision, created_at, updated_at)
            VALUES (?, ?, ?, 0, 0, ?, ?)
            ON CONFLICT(id) DO UPDATE SET
                name = excluded.name,
                note = excluded.note,
                updated_at = excluded.updated_at
        """, (bank_uuid, bank_name, bank_note, created_at, created_at))

        # Associate senses
        for item in chunk:
            w = item["word"]
            sid = word_to_sense.get(w)
            if sid:
                cur.execute("""
                    INSERT OR IGNORE INTO bank_sense (bank_id, sense_id, added_at)
                    VALUES (?, ?, ?)
                """, (bank_uuid, sid, created_at))

        # Advance revision
        cur.execute("UPDATE bank SET revision = revision + 1 WHERE id = ?", (bank_uuid,))

        # Export day CSV and TXT
        day_csv = DAILY_DIR / f"cet6_day{day_idx:02d}.csv"
        day_txt = DAILY_DIR / f"cet6_day{day_idx:02d}.txt"

        with open(day_csv, "w", encoding="utf-8", newline="") as f:
            wtr = csv.writer(f)
            wtr.writerow(["word", "pos", "meaning"])
            for e in chunk:
                wtr.writerow([e["word"], e["pos"], e["meaning"]])

        with open(day_txt, "w", encoding="utf-8") as f:
            for e in chunk:
                f.write(e["word"] + "\n")

        created_banks.append({
            "day": day_idx,
            "id": bank_uuid,
            "name": bank_name,
            "count": count,
            "range": f"{first_word} ~ {last_word}"
        })
        day_idx += 1

    conn.commit()
    conn.close()

    print(f"Successfully generated {len(created_banks)} daily banks!")
    print(f"Sample: Day 01 -> {created_banks[0]['name']} [{created_banks[0]['range']}]")
    print(f"Sample: Day 27 -> {created_banks[-1]['name']} [{created_banks[-1]['range']}]")


if __name__ == "__main__":
    main()
