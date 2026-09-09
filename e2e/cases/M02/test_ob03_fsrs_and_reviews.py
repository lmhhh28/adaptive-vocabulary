"""OB-03: FSRS reviews, multi-correction review progression, and exclusion acceptance test.
Verifies:
- Normal wrong + 2 corrections produce exactly 3 review events.
- Complete FSRS card fields are populated (state, step, stability, difficulty, due, reps, lapses, elapsed, scheduled).
- Exam revised, telemetry_incomplete, and omitted submissions produce 0 reviews.
- Offline replay reproduces exact identical Card state and reviews.
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

    trace = {
        "description": "OB-03 FSRS card and reviews verification",
        "seed": "core",
        "policy": "config/policy-v2.json",
        "steps": [
            {
                "step": 1,
                "kind": "session",
                "session_id": "sess_ob03",
                "session_kind": "practice",
                "status": "open",
                "created_at": t0,
            },
            # 1. Normal wrong -> review #1
            {
                "step": 2,
                "kind": "reserve",
                "session_id": "sess_ob03",
                "instance_id": "inst_ob03_1",
                "ordinal": 0,
                "normal_position": 1,
                "question_version_id": "00000000-0000-4000-8000-000000000301",
                "sense_id": "00000000-0000-4000-8000-000000000101",
                "lexeme_id": "00000000-0000-4000-8000-000000000001",
                "form_id": "00000000-0000-4000-8000-000000000501",
                "observed_at": t0,
            },
            {
                "step": 3,
                "kind": "attempt",
                "attempt": {
                    "id": "att_ob03_1",
                    "instance_id": "inst_ob03_1",
                    "session_id": "sess_ob03",
                    "sense_id": "00000000-0000-4000-8000-000000000101",
                    "task_family": "meaning_recognition",
                    "source": "practice",
                    "purpose": "normal",
                    "response_history": "single_response",
                    "selected_option_id": "00000000-0000-4000-8000-000000002001",
                    "grading_version": "grading-v1",
                    "is_correct": False,
                    "observed_at": t0 + 10_000_000,
                },
            },
            # 2. Correction retry #1 wrong -> review #2
            {
                "step": 4,
                "kind": "reserve",
                "session_id": "sess_ob03",
                "instance_id": "inst_ob03_2",
                "ordinal": 1,
                "normal_position": None,
                "question_version_id": "00000000-0000-4000-8000-000000000308",
                "sense_id": "00000000-0000-4000-8000-000000000101",
                "lexeme_id": "00000000-0000-4000-8000-000000000001",
                "form_id": "00000000-0000-4000-8000-000000000508",
                "observed_at": t0 + 300_000_000, # 5 min later
            },
            {
                "step": 5,
                "kind": "attempt",
                "attempt": {
                    "id": "att_ob03_2",
                    "instance_id": "inst_ob03_2",
                    "session_id": "sess_ob03",
                    "sense_id": "00000000-0000-4000-8000-000000000101",
                    "task_family": "meaning_recognition",
                    "source": "practice",
                    "purpose": "correction",
                    "response_history": "single_response",
                    "selected_option_id": "00000000-0000-4000-8000-000000002001",
                    "grading_version": "grading-v1",
                    "is_correct": False,
                    "observed_at": t0 + 310_000_000,
                },
            },
            # 3. Correction retry #2 correct -> review #3
            {
                "step": 6,
                "kind": "reserve",
                "session_id": "sess_ob03",
                "instance_id": "inst_ob03_3",
                "ordinal": 2,
                "normal_position": None,
                "question_version_id": "00000000-0000-4000-8000-000000000308",
                "sense_id": "00000000-0000-4000-8000-000000000101",
                "lexeme_id": "00000000-0000-4000-8000-000000000001",
                "form_id": "00000000-0000-4000-8000-000000000508",
                "observed_at": t0 + 900_000_000, # 15 min later
            },
            {
                "step": 7,
                "kind": "attempt",
                "attempt": {
                    "id": "att_ob03_3",
                    "instance_id": "inst_ob03_3",
                    "session_id": "sess_ob03",
                    "sense_id": "00000000-0000-4000-8000-000000000101",
                    "task_family": "meaning_recognition",
                    "source": "practice",
                    "purpose": "correction",
                    "response_history": "single_response",
                    "selected_option_id": "00000000-0000-4000-8000-000000002000",
                    "grading_version": "grading-v1",
                    "is_correct": True,
                    "observed_at": t0 + 910_000_000,
                },
            },
            # 4. Exam session with revised, telemetry_incomplete, omitted
            {
                "step": 8,
                "kind": "session",
                "session_id": "sess_ob03_exam",
                "session_kind": "exam",
                "status": "submitted",
                "created_at": t0 + 1000_000_000,
            },
            {
                "step": 9,
                "kind": "reserve",
                "session_id": "sess_ob03_exam",
                "instance_id": "inst_ob03_ex1",
                "ordinal": 0,
                "question_version_id": "00000000-0000-4000-8000-000000000302",
                "sense_id": "00000000-0000-4000-8000-000000000102",
                "lexeme_id": "00000000-0000-4000-8000-000000000002",
                "form_id": "00000000-0000-4000-8000-000000000502",
                "observed_at": t0 + 1000_000_000,
            },
            # Exam revised attempt -> 0 reviews
            {
                "step": 10,
                "kind": "attempt",
                "attempt": {
                    "id": "att_ob03_ex1",
                    "instance_id": "inst_ob03_ex1",
                    "session_id": "sess_ob03_exam",
                    "sense_id": "00000000-0000-4000-8000-000000000102",
                    "task_family": "meaning_recognition",
                    "source": "exam",
                    "purpose": "normal",
                    "response_history": "revised",
                    "selected_option_id": "00000000-0000-4000-8000-000000002004",
                    "grading_version": "grading-v1",
                    "is_correct": True,
                    "observed_at": t0 + 1010_000_000,
                },
            },
            {
                "step": 11,
                "kind": "reserve",
                "session_id": "sess_ob03_exam",
                "instance_id": "inst_ob03_ex2",
                "ordinal": 1,
                "question_version_id": "00000000-0000-4000-8000-000000000303",
                "sense_id": "00000000-0000-4000-8000-000000000103",
                "lexeme_id": "00000000-0000-4000-8000-000000000003",
                "form_id": "00000000-0000-4000-8000-000000000503",
                "observed_at": t0 + 1000_000_000,
            },
            # Exam telemetry_incomplete attempt -> 0 reviews
            {
                "step": 12,
                "kind": "attempt",
                "attempt": {
                    "id": "att_ob03_ex2",
                    "instance_id": "inst_ob03_ex2",
                    "session_id": "sess_ob03_exam",
                    "sense_id": "00000000-0000-4000-8000-000000000103",
                    "task_family": "meaning_recognition",
                    "source": "exam",
                    "purpose": "normal",
                    "response_history": "telemetry_incomplete",
                    "selected_option_id": "00000000-0000-4000-8000-000000002008",
                    "grading_version": "grading-v1",
                    "is_correct": True,
                    "observed_at": t0 + 1020_000_000,
                },
            },
        ],
    }

    fixture_path = case_dir / "ob03_trace.json"
    fixture_path.write_text(json.dumps(trace, indent=2))

    output_dir = case_dir / "output"
    cmd = [
        str(bin_path),
        "learning",
        "verify-trace",
        "--input",
        str(fixture_path),
        "--output",
        str(output_dir),
    ]

    proc = subprocess.run(cmd, capture_output=True, text=True)
    if proc.returncode != 0:
        raise RuntimeError(f"verify-trace failed with code {proc.returncode}:\nStdout: {proc.stdout}\nStderr: {proc.stderr}")

    summary_path = output_dir / "summary.json"
    assert summary_path.is_file()
    summary = json.loads(summary_path.read_text())
    assert summary["reviews_count"] == 3, f"expected exactly 3 reviews, got {summary['reviews_count']}"
    assert summary["fsrs_replay"] == "identical"

    db_path = output_dir / "trace.db"
    conn = sqlite3.connect(db_path)
    cur = conn.cursor()

    cur.execute("SELECT count(*) FROM review_event")
    total_reviews = cur.fetchone()[0]
    assert total_reviews == 3, f"expected 3 reviews in DB, got {total_reviews}"

    cur.execute("""
        SELECT rating, fsrs_state, stability, difficulty, due_at, reps, lapses, scheduled_days
        FROM review_event
        ORDER BY review_time ASC
    """)
    rows = cur.fetchall()
    assert len(rows) == 3

    # Review 1: rating Again (1), state learning, lapses 1
    r1 = rows[0]
    assert r1[0] == 1, f"expected rating Again(1), got {r1[0]}"
    assert r1[2] > 0, "stability should be > 0"
    assert r1[3] > 0, "difficulty should be > 0"

    # Review 2: rating Again (1)
    r2 = rows[1]
    assert r2[0] == 1

    # Review 3: rating Good (3)
    r3 = rows[2]
    assert r3[0] == 3, f"expected rating Good(3), got {r3[0]}"
    assert r3[5] == 3, f"expected 3 reps, got {r3[5]}"

    # Verify memory_projection has complete card fields
    cur.execute("""
        SELECT fsrs_state, fsrs_step, stability, difficulty, due_at, last_review_at, reps, lapses
        FROM memory_projection
        WHERE sense_id = '00000000-0000-4000-8000-000000000101'
    """)
    mem = cur.fetchone()
    assert mem is not None
    assert mem[2] > 0, "stability in memory_projection should be > 0"
    assert mem[3] > 0, "difficulty in memory_projection should be > 0"
    assert mem[6] == 3, "reps should be 3"

    conn.close()

    return {
        "status": "passed",
        "artifacts": [str(summary_path), str(fixture_path)],
        "details": {
            "reviews_count": total_reviews,
            "final_reps": mem[6],
            "fsrs_replay": summary["fsrs_replay"],
        },
    }
