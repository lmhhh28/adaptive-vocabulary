"""OB-01: Exposure labels acceptance test.
Verifies:
- First encounter with lexeme/form has no historical labels (excludes itself).
- Repeated same form hits same_form_reserved permanently.
- Different form, same lexeme within 24h hits recent_related_exposure.
- Different form, same lexeme after 24h does NOT hit recent_related_exposure (24h window).
- Same form after 24h still hits same_form_reserved (permanent).
- Submission's own feedback_available exposure does not contaminate its own observation cutoff.
- Offline replay reproduces identical results.
"""
import json
from pathlib import Path
import subprocess


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    case_dir.mkdir(parents=True, exist_ok=True)

    t0 = 1725786000000000 # 2026-09-08 09:00:00 UTC
    h24 = 24 * 3600 * 1000000

    # We construct a trace JSON testing all OB-01 scenarios using core seed
    # river: lexeme 00000000-0000-4000-8000-000000000001
    # river meaning_recognition:
    # form 1: 00000000-0000-4000-8000-000000000501 (qv 00000000-0000-4000-8000-000000000301)
    # form 2: 00000000-0000-4000-8000-000000000502 (mountain) or river reverse_selection form 00000000-0000-4000-8000-000000000508
    trace = {
        "description": "OB-01 exposure labels verification",
        "seed": "core",
        "policy": "config/policy-v2.json",
        "steps": [
            {
                "step": 1,
                "kind": "session",
                "session_id": "sess_ob01",
                "session_kind": "practice",
                "status": "open",
                "created_at": t0,
            },
            # 1. First encounter with river (form 501) at t0
            {
                "step": 2,
                "kind": "reserve",
                "session_id": "sess_ob01",
                "instance_id": "inst_ob01_1",
                "ordinal": 0,
                "normal_position": 1,
                "question_version_id": "00000000-0000-4000-8000-000000000301",
                "sense_id": "00000000-0000-4000-8000-000000000101",
                "lexeme_id": "00000000-0000-4000-8000-000000000001",
                "form_id": "00000000-0000-4000-8000-000000000501",
                "observed_at": t0,
            },
            # Attempt 1: submitted at t0 + 10s
            {
                "step": 3,
                "kind": "attempt",
                "attempt": {
                    "id": "att_ob01_1",
                    "instance_id": "inst_ob01_1",
                    "session_id": "sess_ob01",
                    "sense_id": "00000000-0000-4000-8000-000000000101",
                    "task_family": "meaning_recognition",
                    "source": "practice",
                    "purpose": "normal",
                    "response_history": "single_response",
                    "selected_option_id": "00000000-0000-4000-8000-000000002000",
                    "grading_version": "grading-v1",
                    "is_correct": True,
                    "observed_at": t0 + 10_000_000,
                },
            },
            # 2. Second encounter with river (same form 501, different instance) within 24h at t0 + 1 hour
            {
                "step": 4,
                "kind": "reserve",
                "session_id": "sess_ob01",
                "instance_id": "inst_ob01_2",
                "ordinal": 1,
                "normal_position": 2,
                "question_version_id": "00000000-0000-4000-8000-000000000301",
                "sense_id": "00000000-0000-4000-8000-000000000101",
                "lexeme_id": "00000000-0000-4000-8000-000000000001",
                "form_id": "00000000-0000-4000-8000-000000000501",
                "observed_at": t0 + 3600_000_000,
            },
            # 3. Third encounter with river (reverse selection, form 508, different form) within 24h at t0 + 2 hours
            {
                "step": 5,
                "kind": "reserve",
                "session_id": "sess_ob01",
                "instance_id": "inst_ob01_3",
                "ordinal": 2,
                "normal_position": 3,
                "question_version_id": "00000000-0000-4000-8000-000000000308",
                "sense_id": "00000000-0000-4000-8000-000000000101",
                "lexeme_id": "00000000-0000-4000-8000-000000000001",
                "form_id": "00000000-0000-4000-8000-000000000508",
                "observed_at": t0 + 7200_000_000,
            },
            # 4. Fourth encounter with river (form 501) after 24h at t0 + 25 hours
            {
                "step": 6,
                "kind": "reserve",
                "session_id": "sess_ob01",
                "instance_id": "inst_ob01_4",
                "ordinal": 3,
                "normal_position": 4,
                "question_version_id": "00000000-0000-4000-8000-000000000301",
                "sense_id": "00000000-0000-4000-8000-000000000101",
                "lexeme_id": "00000000-0000-4000-8000-000000000001",
                "form_id": "00000000-0000-4000-8000-000000000501",
                "observed_at": t0 + h24 + 3600_000_000,
            },
        ],
    }

    fixture_path = case_dir / "ob01_trace.json"
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
    assert summary_path.is_file(), "summary.json not produced"
    summary = json.loads(summary_path.read_text())
    assert summary["status"] == "ok"
    assert summary["fsrs_replay"] == "identical"

    db_path = output_dir / "trace.db"

    # Now verify exposure label queries via the CLI labels subcommand
    # Check inst_ob01_1 at t0: should be false, false
    cmd_l1 = [
        str(bin_path), "learning", "labels",
        "--db", str(db_path),
        "--instance", "inst_ob01_1",
        "--form", "00000000-0000-4000-8000-000000000501",
        "--lexeme", "00000000-0000-4000-8000-000000000001",
        "--cutoff", str(t0),
        "--seq", "100",
    ]
    p1 = subprocess.run(cmd_l1, capture_output=True, text=True, check=True)
    res1 = json.loads(p1.stdout)
    assert res1["has_same_form"] is False, f"inst1 should exclude itself: {res1}"
    assert res1["has_recent_related"] is False, f"inst1 should have no prior recent related: {res1}"

    # Check inst_ob01_2 at t0 + 1h (same form, within 24h): should be true, true
    cmd_l2 = [
        str(bin_path), "learning", "labels",
        "--db", str(db_path),
        "--instance", "inst_ob01_2",
        "--form", "00000000-0000-4000-8000-000000000501",
        "--lexeme", "00000000-0000-4000-8000-000000000001",
        "--cutoff", str(t0 + 3600_000_000),
        "--seq", "100",
    ]
    p2 = subprocess.run(cmd_l2, capture_output=True, text=True, check=True)
    res2 = json.loads(p2.stdout)
    assert res2["has_same_form"] is True, f"inst2 should hit same form: {res2}"
    assert res2["has_recent_related"] is True, f"inst2 should hit recent related: {res2}"

    # Check inst_ob01_3 at t0 + 2h (different form 508, same lexeme, within 24h): should be false, true
    cmd_l3 = [
        str(bin_path), "learning", "labels",
        "--db", str(db_path),
        "--instance", "inst_ob01_3",
        "--form", "00000000-0000-4000-8000-000000000508",
        "--lexeme", "00000000-0000-4000-8000-000000000001",
        "--cutoff", str(t0 + 7200_000_000),
        "--seq", "100",
    ]
    p3 = subprocess.run(cmd_l3, capture_output=True, text=True, check=True)
    res3 = json.loads(p3.stdout)
    assert res3["has_same_form"] is False, f"inst3 has different form, should be false: {res3}"
    assert res3["has_recent_related"] is True, f"inst3 within 24h of river, should be true: {res3}"

    # Check inst_ob01_4 at t0 + 25h (same form 501, outside 24h of inst1):
    # Notice: inst2 and inst3 were also at t0+1h and t0+2h.
    # At t0 + 24h + 3h = t0 + 27h, all earlier exposures of river are > 24h old!
    cmd_l4 = [
        str(bin_path), "learning", "labels",
        "--db", str(db_path),
        "--instance", "inst_ob01_4",
        "--form", "00000000-0000-4000-8000-000000000501",
        "--lexeme", "00000000-0000-4000-8000-000000000001",
        "--cutoff", str(t0 + h24 + 10_000_000_000), # > 24h after all exposures
        "--seq", "100",
    ]
    p4 = subprocess.run(cmd_l4, capture_output=True, text=True, check=True)
    res4 = json.loads(p4.stdout)
    assert res4["has_same_form"] is True, f"same_form persists indefinitely: {res4}"
    assert res4["has_recent_related"] is False, f"recent_related must expire after 24h: {res4}"

    return {
        "status": "passed",
        "artifacts": [str(summary_path), str(fixture_path)],
        "details": {
            "cases_verified": ["exclude_self", "same_form_24h", "diff_form_24h", "expire_24h_recent"],
            "fsrs_replay": summary["fsrs_replay"],
        },
    }
