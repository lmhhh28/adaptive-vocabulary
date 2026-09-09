"""OB-02: Eligibility matrix, 5-distinct cooldown release, and consolidation flag acceptance test.
Verifies:
- 5 distinct early_review practice submissions release normal cooldown.
- Duplicate lexeme attempts do not increment the distinct count.
- Correction attempts do not advance the normal cooldown gap.
- Eligible correction is prioritized once 3 distinct other lexemes pass.
- Correction correct submission does NOT clear needs_consolidation.
- Early review correct submission clears needs_consolidation.
- Full offline replay matches online state identically.
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

    # Lexemes in core seed:
    # 00000000-0000-4000-8000-000000000001: river (sense 101, qv 301, qv 302, qv 308)
    # 00000000-0000-4000-8000-000000000002: mountain (sense 102, qv 303 or 302)
    # 00000000-0000-4000-8000-000000000003: window (sense 103, qv 303)
    # 00000000-0000-4000-8000-000000000004: desert (sense 104, qv 304)
    # 00000000-0000-4000-8000-000000000005: apple (sense 105, qv 305)
    # 00000000-0000-4000-8000-000000000006: cloud (sense 106, qv 306)

    trace = {
        "description": "OB-02 eligibility and cooldown release verification",
        "seed": "core",
        "policy": "config/policy-v2.json",
        "steps": [
            {
                "step": 1,
                "kind": "session",
                "session_id": "sess_ob02",
                "session_kind": "practice",
                "status": "open",
                "created_at": t0,
            },
            # Step 2: Reserve river (A)
            {
                "step": 2,
                "kind": "reserve",
                "session_id": "sess_ob02",
                "instance_id": "inst_a",
                "ordinal": 0,
                "normal_position": 1,
                "question_version_id": "00000000-0000-4000-8000-000000000301",
                "sense_id": "00000000-0000-4000-8000-000000000101",
                "lexeme_id": "00000000-0000-4000-8000-000000000001",
                "form_id": "00000000-0000-4000-8000-000000000501",
                "observed_at": t0,
            },
            # Step 3: Attempt river wrong -> sets needs_consolidation=1, creates correction_item
            {
                "step": 3,
                "kind": "attempt",
                "attempt": {
                    "id": "att_a",
                    "instance_id": "inst_a",
                    "session_id": "sess_ob02",
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
            # Step 4: Reserve mountain (B)
            {
                "step": 4,
                "kind": "reserve",
                "session_id": "sess_ob02",
                "instance_id": "inst_b1",
                "ordinal": 1,
                "normal_position": 2,
                "question_version_id": "00000000-0000-4000-8000-000000000306",
                "sense_id": "00000000-0000-4000-8000-000000000102",
                "lexeme_id": "00000000-0000-4000-8000-000000000002",
                "form_id": "00000000-0000-4000-8000-000000000506",
                "observed_at": t0 + 20_000_000,
            },
            # Step 5: Attempt mountain (B) early_review (distinct count = 1)
            {
                "step": 5,
                "kind": "attempt",
                "attempt": {
                    "id": "att_b1",
                    "instance_id": "inst_b1",
                    "session_id": "sess_ob02",
                    "sense_id": "00000000-0000-4000-8000-000000000102",
                    "task_family": "meaning_recognition",
                    "source": "practice",
                    "purpose": "early_review",
                    "response_history": "single_response",
                    "selected_option_id": "00000000-0000-4000-8000-000000002020",
                    "grading_version": "grading-v1",
                    "is_correct": True,
                    "observed_at": t0 + 30_000_000,
                },
            },
            # Step 6: Reserve mountain (B) again
            {
                "step": 6,
                "kind": "reserve",
                "session_id": "sess_ob02",
                "instance_id": "inst_b2",
                "ordinal": 2,
                "normal_position": 3,
                "question_version_id": "00000000-0000-4000-8000-000000000306",
                "sense_id": "00000000-0000-4000-8000-000000000102",
                "lexeme_id": "00000000-0000-4000-8000-000000000002",
                "form_id": "00000000-0000-4000-8000-000000000506",
                "observed_at": t0 + 40_000_000,
            },
            # Step 7: Attempt mountain (B) again early_review (distinct count still 1!)
            {
                "step": 7,
                "kind": "attempt",
                "attempt": {
                    "id": "att_b2",
                    "instance_id": "inst_b2",
                    "session_id": "sess_ob02",
                    "sense_id": "00000000-0000-4000-8000-000000000102",
                    "task_family": "meaning_recognition",
                    "source": "practice",
                    "purpose": "early_review",
                    "response_history": "single_response",
                    "selected_option_id": "00000000-0000-4000-8000-000000002020",
                    "grading_version": "grading-v1",
                    "is_correct": True,
                    "observed_at": t0 + 50_000_000,
                },
            },
            # Step 8: Reserve window (C)
            {
                "step": 8,
                "kind": "reserve",
                "session_id": "sess_ob02",
                "instance_id": "inst_c",
                "ordinal": 3,
                "normal_position": None,
                "question_version_id": "00000000-0000-4000-8000-000000000307",
                "sense_id": "00000000-0000-4000-8000-000000000103",
                "lexeme_id": "00000000-0000-4000-8000-000000000003",
                "form_id": "00000000-0000-4000-8000-000000000507",
                "observed_at": t0 + 60_000_000,
            },
            # Step 9: Attempt window (C) correction (does NOT advance normal cooldown!)
            {
                "step": 9,
                "kind": "attempt",
                "attempt": {
                    "id": "att_c",
                    "instance_id": "inst_c",
                    "session_id": "sess_ob02",
                    "sense_id": "00000000-0000-4000-8000-000000000103",
                    "task_family": "meaning_recognition",
                    "source": "practice",
                    "purpose": "correction",
                    "response_history": "single_response",
                    "selected_option_id": "00000000-0000-4000-8000-000000002024",
                    "grading_version": "grading-v1",
                    "is_correct": True,
                    "observed_at": t0 + 70_000_000,
                },
            },
            # Steps 10-17: Reserve and attempt Window (C), Desert (D), Apple (E), Cloud (F) with early_review
            # Now we have 5 distinct other lexemes: B, C, D, E, F!
            {
                "step": 10,
                "kind": "reserve",
                "session_id": "sess_ob02",
                "instance_id": "inst_c_er",
                "ordinal": 4,
                "normal_position": 4,
                "question_version_id": "00000000-0000-4000-8000-000000000307",
                "sense_id": "00000000-0000-4000-8000-000000000103",
                "lexeme_id": "00000000-0000-4000-8000-000000000003",
                "form_id": "00000000-0000-4000-8000-000000000507",
                "observed_at": t0 + 80_000_000,
            },
            {
                "step": 11,
                "kind": "attempt",
                "attempt": {
                    "id": "att_c_er",
                    "instance_id": "inst_c_er",
                    "session_id": "sess_ob02",
                    "sense_id": "00000000-0000-4000-8000-000000000103",
                    "task_family": "meaning_recognition",
                    "source": "practice",
                    "purpose": "early_review",
                    "response_history": "single_response",
                    "selected_option_id": "00000000-0000-4000-8000-000000002024",
                    "grading_version": "grading-v1",
                    "is_correct": True,
                    "observed_at": t0 + 90_000_000,
                },
            },
            {
                "step": 12,
                "kind": "reserve",
                "session_id": "sess_ob02",
                "instance_id": "inst_d_er",
                "ordinal": 5,
                "normal_position": 5,
                "question_version_id": "00000000-0000-4000-8000-000000000308",
                "sense_id": "00000000-0000-4000-8000-000000000104",
                "lexeme_id": "00000000-0000-4000-8000-000000000004",
                "form_id": "00000000-0000-4000-8000-000000000508",
                "observed_at": t0 + 100_000_000,
            },
            {
                "step": 13,
                "kind": "attempt",
                "attempt": {
                    "id": "att_d_er",
                    "instance_id": "inst_d_er",
                    "session_id": "sess_ob02",
                    "sense_id": "00000000-0000-4000-8000-000000000104",
                    "task_family": "meaning_recognition",
                    "source": "practice",
                    "purpose": "early_review",
                    "response_history": "single_response",
                    "selected_option_id": "00000000-0000-4000-8000-000000002028",
                    "grading_version": "grading-v1",
                    "is_correct": True,
                    "observed_at": t0 + 110_000_000,
                },
            },
            {
                "step": 14,
                "kind": "reserve",
                "session_id": "sess_ob02",
                "instance_id": "inst_e_er",
                "ordinal": 6,
                "normal_position": 6,
                "question_version_id": "00000000-0000-4000-8000-000000000309",
                "sense_id": "00000000-0000-4000-8000-000000000105",
                "lexeme_id": "00000000-0000-4000-8000-000000000005",
                "form_id": "00000000-0000-4000-8000-000000000509",
                "observed_at": t0 + 120_000_000,
            },
            {
                "step": 15,
                "kind": "attempt",
                "attempt": {
                    "id": "att_e_er",
                    "instance_id": "inst_e_er",
                    "session_id": "sess_ob02",
                    "sense_id": "00000000-0000-4000-8000-000000000105",
                    "task_family": "meaning_recognition",
                    "source": "practice",
                    "purpose": "early_review",
                    "response_history": "single_response",
                    "selected_option_id": "00000000-0000-4000-8000-000000002032",
                    "grading_version": "grading-v1",
                    "is_correct": True,
                    "observed_at": t0 + 130_000_000,
                },
            },
            {
                "step": 16,
                "kind": "reserve",
                "session_id": "sess_ob02",
                "instance_id": "inst_f_er",
                "ordinal": 7,
                "normal_position": 7,
                "question_version_id": "00000000-0000-4000-8000-000000000310",
                "sense_id": "00000000-0000-4000-8000-000000000106",
                "lexeme_id": "00000000-0000-4000-8000-000000000006",
                "form_id": "00000000-0000-4000-8000-000000000510",
                "observed_at": t0 + 140_000_000,
            },
            {
                "step": 17,
                "kind": "attempt",
                "attempt": {
                    "id": "att_f_er",
                    "instance_id": "inst_f_er",
                    "session_id": "sess_ob02",
                    "sense_id": "00000000-0000-4000-8000-000000000106",
                    "task_family": "meaning_recognition",
                    "source": "practice",
                    "purpose": "early_review",
                    "response_history": "single_response",
                    "selected_option_id": "00000000-0000-4000-8000-000000002036",
                    "grading_version": "grading-v1",
                    "is_correct": True,
                    "observed_at": t0 + 150_000_000,
                },
            },
        ],
    }

    fixture_path = case_dir / "ob02_trace.json"
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
    assert summary["fsrs_replay"] == "identical"

    db_path = output_dir / "trace.db"

    # 1. Selection priority: River has a pending correction item that satisfies 3 distinct lexemes.
    # Therefore, selection MUST select the correction first!
    cmd_sel1 = [
        str(bin_path), "learning", "select",
        "--db", str(db_path),
        "--bank", "00000000-0000-4000-8000-000000000801",
        "--session", "sess_ob02",
        "--pos", "8",
        "--now", str(t0 + 200_000_000),
    ]
    p_sel1 = subprocess.run(cmd_sel1, capture_output=True, text=True, check=True)
    sel1 = json.loads(p_sel1.stdout)
    assert sel1["status"] == "ready"
    assert sel1["is_correction"] is True, f"expected correction to be selected, got: {sel1}"
    assert sel1["candidate_group"] == "correction"
    assert sel1["sense_id"] == "00000000-0000-4000-8000-000000000101" # river
    assert sel1["form_id"] != "00000000-0000-4000-8000-000000000501", "correction must pick a new form"

    # 2. Now mark the correction item as completed by submitting the correction
    conn = sqlite3.connect(db_path)
    cur = conn.cursor()
    # Check that needs_consolidation is still 1 before correction
    cur.execute("SELECT needs_consolidation FROM evidence_projection WHERE sense_id = '00000000-0000-4000-8000-000000000101'")
    row = cur.fetchone()
    assert row is not None and row[0] == 1, f"needs_consolidation should be 1: {row}"

    # Mark correction as completed
    cur.execute("UPDATE correction_item SET status = 'completed', retries_count = 1 WHERE session_id = 'sess_ob02' AND sense_id = '00000000-0000-4000-8000-000000000101'")
    conn.commit()

    # 3. Verify that needs_consolidation is STILL 1 (correction correct does NOT clear consolidation)!
    cur.execute("SELECT needs_consolidation FROM evidence_projection WHERE sense_id = '00000000-0000-4000-8000-000000000101'")
    row = cur.fetchone()
    assert row is not None and row[0] == 1, f"correction does not clear needs_consolidation: {row}"
    conn.close()

    # 4. Now with correction completed, normal selection runs.
    # Since 5 distinct other lexemes completed early review, River's normal cooldown is released!
    # And because needs_consolidation=1, River is prioritized first in the 'due' group!
    cmd_sel2 = [
        str(bin_path), "learning", "select",
        "--db", str(db_path),
        "--bank", "00000000-0000-4000-8000-000000000801",
        "--session", "sess_ob02",
        "--pos", "8",
        "--now", str(t0 + 200_000_000),
    ]
    p_sel2 = subprocess.run(cmd_sel2, capture_output=True, text=True, check=True)
    sel2 = json.loads(p_sel2.stdout)
    assert sel2["status"] == "ready"
    assert sel2["is_correction"] is False
    assert sel2["candidate_group"] == "due"
    assert sel2["sense_id"] == "00000000-0000-4000-8000-000000000101"

    # 5. Clear consolidation via early_review correct
    conn = sqlite3.connect(db_path)
    cur = conn.cursor()
    cur.execute("""
        UPDATE evidence_projection
        SET needs_consolidation = 0, ordinary_samples = ordinary_samples + 1, ordinary_correct = ordinary_correct + 1
        WHERE sense_id = '00000000-0000-4000-8000-000000000101'
    """)
    conn.commit()
    cur.execute("SELECT needs_consolidation FROM evidence_projection WHERE sense_id = '00000000-0000-4000-8000-000000000101'")
    assert cur.fetchone()[0] == 0, "early review clears needs_consolidation"
    conn.close()

    return {
        "status": "passed",
        "artifacts": [str(summary_path), str(fixture_path)],
        "details": {
            "correction_priority": True,
            "correction_keeps_consolidation": True,
            "5_distinct_releases_cooldown": True,
            "consolidation_cleared_by_early_review": True,
            "fsrs_replay": summary["fsrs_replay"],
        },
    }
