"""PR-03: State recovery and correction flow acceptance test.
Verifies:
1. Closing page / restarting service recovers the same instance & options;
2. Normal error creates pending correction item;
3. 3-distinct-lexeme interleaving gap before correction retry;
4. First correction retry presents New Form 1, answered wrong (retries=1);
5. Second 3-lexeme interleaving gap;
6. Second correction retry presents New Form 2, answered correctly -> completed!
"""
from pathlib import Path
import sqlite3
import uuid
from support.lifecycle import managed_server
from support.http_client import APIClient


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    data_dir = case_dir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)
    artifacts = []

    bank_id = "00000000-0000-4000-8000-000000000801"

    # =========================================================================
    # Part 1: Server Restart & State Recovery
    # =========================================================================
    session_id = None
    inst_0_id = None
    inst_0_stem = None
    inst_0_opts = None

    with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir / "s1") as server1:
        artifacts.append(str(server1.log_path))
        client = APIClient(server1.base_url)
        client.initialize_client_context()

        # Create practice session
        resp = client.post("/api/v1/practice-sessions", json={"bank_id": bank_id}, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp.status_code == 201
        session_id = resp.json()["session"]["id"]

        # Call next (cursor 0) to reserve instance 0
        resp_next = client.post(f"/api/v1/practice-sessions/{session_id}/next", json={"expected_cursor": 0})
        assert resp_next.status_code == 200
        q0 = resp_next.json()["question"]
        inst_0_id = q0["instance_id"]
        inst_0_stem = q0["stem"]
        inst_0_opts = q0["options"]

        client.close()
        # server1 stops here

    # Restart server on the same data directory
    with managed_server(bin_path, data_dir, listen_port=0, log_dir=case_dir / "s2") as server2:
        artifacts.append(str(server2.log_path))
        client = APIClient(server2.base_url)
        client.initialize_client_context()

        # Active session should still be open
        resp_active = client.get("/api/v1/sessions/active")
        assert resp_active.status_code == 200
        active_data = resp_active.json()["session"]
        assert active_data is not None
        assert active_data["id"] == session_id
        assert active_data["cursor"] == 0

        # Query session detail: current_question should be instance 0
        resp_detail = client.get(f"/api/v1/practice-sessions/{session_id}")
        assert resp_detail.status_code == 200
        detail = resp_detail.json()
        assert detail["current_question"] is not None
        assert detail["current_question"]["instance_id"] == inst_0_id
        assert detail["current_question"]["stem"] == inst_0_stem

        # Calling next with expected_cursor: 0 should return the exact same instance 0
        resp_next_rec = client.post(f"/api/v1/practice-sessions/{session_id}/next", json={"expected_cursor": 0})
        assert resp_next_rec.status_code == 200
        assert resp_next_rec.json()["question"]["instance_id"] == inst_0_id

        # =====================================================================
        # Part 2: Correction Flow (Normal Wrong -> Interleave -> Retry 1 -> Interleave -> Retry 2)
        # =====================================================================
        db_file = data_dir / "app.db"
        conn = sqlite3.connect(str(db_file))
        cur = conn.cursor()

        # Find correct option for instance 0 from question_version
        cur.execute("""
            SELECT qv.correct_option_id, qv.form_id, qv.sense_id
            FROM question_instance qi
            JOIN question_version qv ON qv.id = qi.question_version_id
            WHERE qi.id = ?
        """, (inst_0_id,))
        row0 = cur.fetchone()
        correct_opt_0, form_0, river_sense_id = row0[0], row0[1], row0[2]

        # Pick a WRONG option for instance 0
        wrong_opt_0 = next(o["id"] for o in inst_0_opts if o["id"] != correct_opt_0)

        # Submit WRONG answer for instance 0
        resp_sub0 = client.post(f"/api/v1/practice-sessions/{session_id}/attempts", json={
            "instance_id": inst_0_id,
            "option_id": wrong_opt_0,
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp_sub0.status_code == 200
        assert resp_sub0.json()["correct"] is False

        # Verify correction_item is created with status='pending', retries_count=0
        cur.execute("SELECT status, retries_count FROM correction_item WHERE session_id = ? AND sense_id = ?", (session_id, river_sense_id))
        corr_row = cur.fetchone()
        assert corr_row is not None, "Expected correction_item to be created"
        assert corr_row[0] == "pending"
        assert corr_row[1] == 0

        # Interleaving 1: Answer 3 questions of distinct other lexemes
        answered_lexemes = set()
        cursor = 1

        for _ in range(3):
            resp_n = client.post(f"/api/v1/practice-sessions/{session_id}/next", json={"expected_cursor": cursor})
            assert resp_n.status_code == 200
            q = resp_n.json()["question"]
            cur.execute("""
                SELECT qv.correct_option_id, s.lexeme_id
                FROM question_instance qi
                JOIN question_version qv ON qv.id = qi.question_version_id
                JOIN sense s ON s.id = qv.sense_id
                WHERE qi.id = ?
            """, (q["instance_id"],))
            corr_id, lex_id = cur.fetchone()
            answered_lexemes.add(lex_id)

            # Submit answer
            resp_sub = client.post(f"/api/v1/practice-sessions/{session_id}/attempts", json={
                "instance_id": q["instance_id"],
                "option_id": corr_id,
            }, headers={"Idempotency-Key": str(uuid.uuid4())})
            assert resp_sub.status_code == 200
            cursor += 1

        assert len(answered_lexemes) == 3, f"Expected 3 distinct lexemes answered, got {len(answered_lexemes)}"

        # Now correction cooling is satisfied (3 distinct other lexemes)!
        # Next question MUST be the correction question for river!
        resp_corr1 = client.post(f"/api/v1/practice-sessions/{session_id}/next", json={"expected_cursor": cursor})
        assert resp_corr1.status_code == 200
        corr1_q = resp_corr1.json()["question"]

        cur.execute("""
            SELECT qv.correct_option_id, qv.form_id, qv.sense_id, qi.normal_position
            FROM question_instance qi
            JOIN question_version qv ON qv.id = qi.question_version_id
            WHERE qi.id = ?
        """, (corr1_q["instance_id"],))
        corr1_row = cur.fetchone()
        corr1_correct_opt, corr1_form, corr1_sense, corr1_norm_pos = corr1_row[0], corr1_row[1], corr1_row[2], corr1_row[3]

        assert corr1_sense == river_sense_id, f"Expected correction question for river, got sense {corr1_sense}"
        assert corr1_form != form_0, f"Expected a NEW form for correction 1, got same form {corr1_form}"
        assert corr1_norm_pos is None, "Correction instances must have normal_position == NULL"

        # Answer correction 1 with WRONG option again (retry 1)
        corr1_wrong_opt = next(o["id"] for o in corr1_q["options"] if o["id"] != corr1_correct_opt)
        resp_sub_corr1 = client.post(f"/api/v1/practice-sessions/{session_id}/attempts", json={
            "instance_id": corr1_q["instance_id"],
            "option_id": corr1_wrong_opt,
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp_sub_corr1.status_code == 200
        assert resp_sub_corr1.json()["correct"] is False
        cursor += 1

        # Check DB: retries_count is now 1, status still pending
        cur.execute("SELECT status, retries_count FROM correction_item WHERE session_id = ? AND sense_id = ?", (session_id, river_sense_id))
        corr_row2 = cur.fetchone()
        assert corr_row2[0] == "pending"
        assert corr_row2[1] == 1

        # Interleaving 2: Answer 3 questions of distinct other lexemes again
        answered_lexemes_2 = set()
        for _ in range(3):
            resp_n = client.post(f"/api/v1/practice-sessions/{session_id}/next", json={"expected_cursor": cursor})
            assert resp_n.status_code == 200
            q = resp_n.json()["question"]
            cur.execute("""
                SELECT qv.correct_option_id, s.lexeme_id
                FROM question_instance qi
                JOIN question_version qv ON qv.id = qi.question_version_id
                JOIN sense s ON s.id = qv.sense_id
                WHERE qi.id = ?
            """, (q["instance_id"],))
            corr_id, lex_id = cur.fetchone()
            answered_lexemes_2.add(lex_id)

            resp_sub = client.post(f"/api/v1/practice-sessions/{session_id}/attempts", json={
                "instance_id": q["instance_id"],
                "option_id": corr_id,
            }, headers={"Idempotency-Key": str(uuid.uuid4())})
            assert resp_sub.status_code == 200
            cursor += 1

        # Second correction retry: MUST present New Form 2!
        resp_corr2 = client.post(f"/api/v1/practice-sessions/{session_id}/next", json={"expected_cursor": cursor})
        assert resp_corr2.status_code == 200
        corr2_q = resp_corr2.json()["question"]

        cur.execute("""
            SELECT qv.correct_option_id, qv.form_id, qv.sense_id
            FROM question_instance qi
            JOIN question_version qv ON qv.id = qi.question_version_id
            WHERE qi.id = ?
        """, (corr2_q["instance_id"],))
        corr2_row = cur.fetchone()
        corr2_correct_opt, corr2_form, corr2_sense = corr2_row[0], corr2_row[1], corr2_row[2]

        assert corr2_sense == river_sense_id, "Expected second correction question for river"
        assert corr2_form != form_0 and corr2_form != corr1_form, f"Expected second NEW form, got {corr2_form} (seen: {form_0}, {corr1_form})"

        # Answer correction 2 CORRECTLY!
        resp_sub_corr2 = client.post(f"/api/v1/practice-sessions/{session_id}/attempts", json={
            "instance_id": corr2_q["instance_id"],
            "option_id": corr2_correct_opt,
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert resp_sub_corr2.status_code == 200
        assert resp_sub_corr2.json()["correct"] is True

        # Check DB: correction_item is completed!
        cur.execute("SELECT status, retries_count FROM correction_item WHERE session_id = ? AND sense_id = ?", (session_id, river_sense_id))
        corr_row3 = cur.fetchone()
        assert corr_row3[0] == "completed", f"Expected completed, got {corr_row3[0]}"
        assert corr_row3[1] == 2

        # Check evidence projection: correction_correct is 1, correction_samples is 2
        cur.execute("SELECT correction_samples, correction_correct, needs_consolidation FROM evidence_projection WHERE sense_id = ?", (river_sense_id,))
        ev_row = cur.fetchone()
        assert ev_row[0] == 2, f"Expected 2 correction samples, got {ev_row[0]}"
        assert ev_row[1] == 1, f"Expected 1 correction correct, got {ev_row[1]}"
        assert ev_row[2] == 1, "Per R05, correction success must NOT clear needs_consolidation flag"

        conn.close()
        client.close()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "restart_recovery_verified": True,
            "normal_wrong_opened_correction": True,
            "two_new_forms_correction_completed": True,
            "r05_needs_consolidation_preserved": True,
        },
    }
