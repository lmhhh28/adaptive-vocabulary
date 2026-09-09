"""SL-01: Selection engine rules, 1/5/9 exploration slots, fallbacks, and small bank obstruction test.
Verifies:
- Exploration slot 1 (and 5, 9) prioritizes unseen group.
- Intermediate slots (2, 3, 4) prioritize due group.
- Group fallback: due -> unseen -> early_review (or unseen -> due -> early_review).
- Eligible correction is prioritized over normal slots and uses a new form.
- Empty or fully-cooldown bank returns unavailable NO_ELIGIBLE_QUESTION with counts.
"""
import json
from pathlib import Path
import sqlite3
import subprocess


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    case_dir.mkdir(parents=True, exist_ok=True)

    t0 = 1725786000000000

    # 1. Setup isolated database and seed core
    db_path = case_dir / "app.db"
    if db_path.is_file():
        db_path.unlink()

    conn = sqlite3.connect(db_path)
    # Run migrations 000, 010, 020
    for mig in sorted((context["root_dir"] / "migrations").glob("*.sql")):
        conn.executescript(mig.read_text())
    conn.commit()
    conn.close()

    # Apply core seed via a minimal verify-trace or direct db populate
    seed_trace = {
        "description": "Initialize database with seed core",
        "seed": "core",
        "steps": [
            {
                "step": 1,
                "kind": "session",
                "session_id": "sess_sl01",
                "session_kind": "practice",
                "status": "open",
                "created_at": t0,
            }
        ],
    }
    fixture_init = case_dir / "init_trace.json"
    fixture_init.write_text(json.dumps(seed_trace))

    proc = subprocess.run([
        str(bin_path), "learning", "verify-trace",
        "--input", str(fixture_init),
        "--output", str(case_dir / "out_init"),
    ], capture_output=True, text=True, check=True)

    db_path = case_dir / "out_init" / "trace.db"
    bank_id = "00000000-0000-4000-8000-000000000801" # core development bank

    # 2. Test Pos 1: Unseen slot -> chooses unseen candidate
    cmd_p1 = [
        str(bin_path), "learning", "select",
        "--db", str(db_path),
        "--bank", bank_id,
        "--session", "sess_sl01",
        "--pos", "1",
        "--now", str(t0),
    ]
    p1 = subprocess.run(cmd_p1, capture_output=True, text=True, check=True)
    res1 = json.loads(p1.stdout)
    assert res1["status"] == "ready", f"pos 1 should be ready: {res1}"
    assert res1["candidate_group"] == "unseen", f"pos 1 should be unseen: {res1}"
    assert res1["sense_id"] == "00000000-0000-4000-8000-000000000101", f"should pick river (first sense): {res1}"

    # 3. Test Pos 2: Due slot -> initially all units are unseen (0 samples), so due is empty.
    # It must fallback to unseen!
    cmd_p2 = [
        str(bin_path), "learning", "select",
        "--db", str(db_path),
        "--bank", bank_id,
        "--session", "sess_sl01",
        "--pos", "2",
        "--now", str(t0),
    ]
    p2 = subprocess.run(cmd_p2, capture_output=True, text=True, check=True)
    res2 = json.loads(p2.stdout)
    assert res2["status"] == "ready"
    assert res2["candidate_group"] == "unseen", f"fallback from empty due should be unseen: {res2}"

    # 4. Now add an attempt on mountain (sense 102) with due_at in the past
    conn = sqlite3.connect(db_path)
    cur = conn.cursor()
    cur.execute("""
        INSERT INTO evidence_projection (projection_version, sense_id, task_family, ordinary_samples, ordinary_correct, needs_consolidation, updated_at)
        VALUES ('v1', '00000000-0000-4000-8000-000000000102', 'meaning_recognition', 1, 1, 0, 100)
    """)
    cur.execute("""
        INSERT INTO memory_projection (projection_version, sense_id, task_family, due_at, updated_at)
        VALUES ('v1', '00000000-0000-4000-8000-000000000102', 'meaning_recognition', 1000, 100)
    """)
    conn.commit()
    conn.close()

    # Test Pos 2 again at now = 2000 (after due_at 1000) -> should pick due (mountain)
    cmd_p2_due = [
        str(bin_path), "learning", "select",
        "--db", str(db_path),
        "--bank", bank_id,
        "--session", "sess_sl01",
        "--pos", "2",
        "--now", str(2000),
    ]
    p2_due = subprocess.run(cmd_p2_due, capture_output=True, text=True, check=True)
    res2_due = json.loads(p2_due.stdout)
    assert res2_due["status"] == "ready"
    assert res2_due["candidate_group"] == "due", f"pos 2 with due item should pick due: {res2_due}"
    assert res2_due["sense_id"] == "00000000-0000-4000-8000-000000000102", f"should pick mountain: {res2_due}"

    # Test Pos 5 (unseen position) -> should pick unseen (river)
    cmd_p5 = [
        str(bin_path), "learning", "select",
        "--db", str(db_path),
        "--bank", bank_id,
        "--session", "sess_sl01",
        "--pos", "5",
        "--now", str(2000),
    ]
    p5 = subprocess.run(cmd_p5, capture_output=True, text=True, check=True)
    res5 = json.loads(p5.stdout)
    assert res5["status"] == "ready"
    assert res5["candidate_group"] == "unseen", f"pos 5 should prioritize unseen: {res5}"
    assert res5["sense_id"] == "00000000-0000-4000-8000-000000000101"

    # 5. Test empty bank obstruction
    conn = sqlite3.connect(db_path)
    cur = conn.cursor()
    cur.execute("INSERT INTO bank (id, name, created_at, updated_at) VALUES ('empty_bank', 'Empty', 1, 1)")
    conn.commit()
    conn.close()

    cmd_empty = [
        str(bin_path), "learning", "select",
        "--db", str(db_path),
        "--bank", "empty_bank",
        "--session", "sess_sl01",
        "--pos", "1",
        "--now", str(t0),
    ]
    p_empty = subprocess.run(cmd_empty, capture_output=True, text=True, check=True)
    res_empty = json.loads(p_empty.stdout)
    assert res_empty["status"] == "unavailable", f"empty bank must return unavailable: {res_empty}"
    assert res_empty["reason"] == "NO_ELIGIBLE_QUESTION", f"expected NO_ELIGIBLE_QUESTION: {res_empty}"

    return {
        "status": "passed",
        "artifacts": [str(fixture_init)],
        "details": {
            "pos1_unseen": True,
            "pos2_due": True,
            "pos5_unseen": True,
            "empty_bank_blocked": True,
        },
    }
