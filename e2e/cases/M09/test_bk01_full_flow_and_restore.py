"""BK-01: Full backup, new directory restore, draft/idempotency/report verification.
Verifies:
1. Practice draft session, exam submission, and in-flight claimed generation task are backed up.
2. avocab backup create and verify succeed with manifest and consistent snapshot.
3. avocab backup restore to a new directory successfully recreates operational data.
4. Restored server recovers draft practice session, preserves idempotency, preserves exam reports,
   and resets uncompleted item lease (fencing incremented) ready for new worker claim.
"""
import json
from pathlib import Path
import sqlite3
import subprocess
import uuid

from support.lifecycle import managed_server
from support.http_client import APIClient
from support.provider_server import ProviderServer
from support.worker_process import run_worker


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    src_data_dir = case_dir / "src_data"
    src_data_dir.mkdir(parents=True, exist_ok=True)
    backup_dir = case_dir / "backup"
    restored_data_dir = case_dir / "restored_data"
    artifacts = []

    bank_id = "00000000-0000-4000-8000-000000000801"

    provider = ProviderServer(expected_api_key="bk01-key", scenario_mode="normal_pass")
    provider.start()

    practice_session_id = None
    exam_id = None
    original_exam_result = None
    job_id = None
    pr_ev0_key = str(uuid.uuid4())
    inst0_id = None
    opt0_id = None

    try:
        with managed_server(bin_path, src_data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
            artifacts.append(str(server.log_path))
            client = APIClient(server.base_url)
            client.initialize_client_context()

            # 1. Exam session with submitted result
            ex_resp = client.post(
                "/api/v1/exams",
                json={"bank_id": bank_id, "question_count": 3},
                headers={"Idempotency-Key": str(uuid.uuid4())}
            )
            assert ex_resp.status_code == 201, f"Create exam failed: {ex_resp.text}"
            ex_data = ex_resp.json()
            exam_id = ex_data["exam_id"]
            ex_rev = ex_data["session_revision"]

            conn = sqlite3.connect(str(src_data_dir / "app.db"))
            cur = conn.cursor()
            cur.execute("SELECT id, ordinal, snapshot_json FROM question_instance WHERE session_id = ? ORDER BY ordinal", (exam_id,))
            ex_instances = cur.fetchall()
            conn.close()

            seq = 1
            for q_id, ord_num, snap_str in ex_instances:
                snap = json.loads(snap_str)
                correct_opt_id = snap.get("correct_option_id")
                if not correct_opt_id and snap.get("options"):
                    correct_opt_id = snap["options"][0]["id"]
                ev_id = str(uuid.uuid4())
                ans_resp = client.post(
                    f"/api/v1/exams/{exam_id}/response-events",
                    json={
                        "instance_id": q_id,
                        "action": "select",
                        "option_id": correct_opt_id,
                        "client_event_id": ev_id,
                        "client_sequence": seq,
                        "expected_revision": ex_rev
                    },
                    headers={"Idempotency-Key": ev_id}
                )
                assert ans_resp.status_code == 200
                ex_rev = ans_resp.json()["current_revision"]
                seq += 1

            # Submit exam
            sub_id = str(uuid.uuid4())
            sub_resp = client.post(
                f"/api/v1/exams/{exam_id}/submit",
                json={
                    "expected_revision": ex_rev,
                    "submission_id": sub_id,
                    "last_ack_sequence": seq - 1
                },
                headers={"Idempotency-Key": sub_id}
            )
            assert sub_resp.status_code == 200

            res_resp = client.get(f"/api/v1/exams/{exam_id}/result")
            assert res_resp.status_code == 200
            original_exam_result = res_resp.json()

            # 2. Practice session with in-flight draft question / answer
            pr_resp = client.post(
                "/api/v1/practice-sessions",
                json={"bank_id": bank_id},
                headers={"Idempotency-Key": str(uuid.uuid4())}
            )
            assert pr_resp.status_code == 201, f"Create practice session failed: {pr_resp.text}"
            pr_data = pr_resp.json()
            practice_session_id = pr_data["session"]["id"]

            resp_next = client.post(f"/api/v1/practice-sessions/{practice_session_id}/next", json={"expected_cursor": 0})
            assert resp_next.status_code == 200, f"Next failed: {resp_next.text}"
            q = resp_next.json()["question"]
            inst0_id = q["instance_id"]
            opt0_id = q["options"][0]["id"]

            # Submit answer for question 0 with fixed Idempotency-Key
            ev_resp = client.post(
                f"/api/v1/practice-sessions/{practice_session_id}/attempts",
                json={
                    "instance_id": inst0_id,
                    "option_id": opt0_id
                },
                headers={"Idempotency-Key": pr_ev0_key}
            )
            assert ev_resp.status_code == 200, f"Practice attempt failed: {ev_resp.text}"

            # Request question 1 so there is an active/unanswered question in the open draft session
            resp_next2 = client.post(f"/api/v1/practice-sessions/{practice_session_id}/next", json={"expected_cursor": 1})
            assert resp_next2.status_code == 200, f"Next 2 failed: {resp_next2.text}"

            # 3. Create generation task and run worker with crash_after_gen
            conn = sqlite3.connect(str(src_data_dir / "app.db"))
            cur = conn.cursor()
            cur.execute("""
                SELECT s.id, sv.id
                FROM sense s
                JOIN sense_version sv ON sv.sense_id = s.id
                JOIN bank_sense bs ON bs.sense_id = s.id
                WHERE bs.bank_id = ? LIMIT 1
            """, (bank_id,))
            s_row = cur.fetchone()
            sense_id, sense_version_id = s_row[0], s_row[1]
            conn.close()

            job_resp = client.post(
                f"/api/v1/banks/{bank_id}/generation-jobs",
                json={
                    "sense_refs": [{"sense_id": sense_id, "sense_version_id": sense_version_id}],
                    "generation_config_version": "v1",
                    "quality_policy_version": "v1",
                    "expected_revision": 0
                },
                headers={"Idempotency-Key": str(uuid.uuid4())}
            )
            assert job_resp.status_code == 202
            job_id = job_resp.json()["job_id"]

            profile_path = case_dir / "profile.json"
            profile = {
                "config_version": "generation-v2",
                "budget_policy_version": "budget-v2",
                "mode": "test",
                "quality_policy_version": "quality-v1",
                "production_adapter": "chat-completions-json-v1",
                "generator": {
                    "base_url": provider.base_url,
                    "model": "test-generator",
                    "api_key": "bk01-key",
                    "max_output_tokens": 1024
                },
                "evaluator": {
                    "base_url": provider.base_url,
                    "model": "test-evaluator",
                    "api_key": "bk01-key",
                    "max_output_tokens": 512
                }
            }
            profile_path.write_text(json.dumps(profile, indent=2), encoding="utf-8")

            run_worker(
                data_dir=src_data_dir,
                server_url=server.base_url,
                profile_path=profile_path,
                worker_id="worker-crash-bk01",
                max_items=1,
                crash_after_gen=True
            )

        # Step 2: Create Backup via CLI
        create_cmd = [
            str(bin_path), "backup", "create",
            "--data-dir", str(src_data_dir),
            "--output", str(backup_dir),
            "--include-checkpoints"
        ]
        res_create = subprocess.run(create_cmd, capture_output=True, text=True)
        assert res_create.returncode == 0, f"backup create failed: {res_create.stderr} {res_create.stdout}"
        create_data = json.loads(res_create.stdout)
        assert create_data["status"] == "ok"
        assert (backup_dir / "manifest.json").is_file()
        assert (backup_dir / "app.db").is_file()

        # Step 3: Verify Backup via CLI
        verify_cmd = [str(bin_path), "backup", "verify", "--input", str(backup_dir)]
        res_verify = subprocess.run(verify_cmd, capture_output=True, text=True)
        assert res_verify.returncode == 0, f"backup verify failed: {res_verify.stderr} {res_verify.stdout}"
        verify_data = json.loads(res_verify.stdout)
        assert verify_data["status"] == "ok"

        # Step 4: Restore Backup to brand new directory via CLI
        restore_cmd = [
            str(bin_path), "backup", "restore",
            "--input", str(backup_dir),
            "--target", str(restored_data_dir)
        ]
        res_restore = subprocess.run(restore_cmd, capture_output=True, text=True)
        assert res_restore.returncode == 0, f"backup restore failed: {res_restore.stderr} {res_restore.stdout}"
        restore_data = json.loads(res_restore.stdout)
        assert restore_data["status"] == "ok"
        assert restore_data["reconciliation"]["invalidated_leases_count"] >= 1

        # Step 5: Start server on restored directory and verify state
        with managed_server(bin_path, restored_data_dir, listen_port=0, log_dir=case_dir) as server:
            artifacts.append(str(server.log_path))
            r_client = APIClient(server.base_url)
            r_client.initialize_client_context()

            # 1. Idempotency on practice answer: re-sending the same client_event_id returns 200 with Idempotent-Replayed
            replay_resp = r_client.post(
                f"/api/v1/practice-sessions/{practice_session_id}/attempts",
                json={
                    "instance_id": inst0_id,
                    "option_id": opt0_id
                },
                headers={"Idempotency-Key": pr_ev0_key}
            )
            assert replay_resp.status_code == 200, f"Replay answer failed: {replay_resp.text}"
            assert replay_resp.headers.get("Idempotent-Replayed") == "true" or replay_resp.headers.get("idempotent-replayed") == "true"

            # Check practice session is still open and retrievable
            pr_get = r_client.get(f"/api/v1/practice-sessions/{practice_session_id}")
            assert pr_get.status_code == 200
            assert pr_get.json()["session"]["status"] == "open"
            pr_rev = pr_get.json()["session"]["revision"]

            # End the restored practice session
            end_resp = r_client.post(
                f"/api/v1/practice-sessions/{practice_session_id}/end",
                json={"reason": "ended", "expected_revision": pr_rev},
                headers={"Idempotency-Key": str(uuid.uuid4())}
            )
            assert end_resp.status_code == 200, f"End practice session failed: {end_resp.text}"

            # 2. Verify exam report is identical
            r_ex_resp = r_client.get(f"/api/v1/exams/{exam_id}/result")
            assert r_ex_resp.status_code == 200
            restored_exam_result = r_ex_resp.json()
            assert restored_exam_result["total"] == original_exam_result["total"]
            assert restored_exam_result["correct"] == original_exam_result["correct"]
            assert restored_exam_result["ratio"] == original_exam_result["ratio"]

            # 3. Verify generation item lease reset in SQLite
            conn = sqlite3.connect(str(restored_data_dir / "app.db"))
            cur = conn.cursor()
            cur.execute("SELECT status, lease_owner, fencing_token, attempt_number FROM generation_item WHERE job_id = ?", (job_id,))
            item_row = cur.fetchone()
            assert item_row is not None
            assert item_row[0] == "pending", f"Expected item status pending, got {item_row[0]}"
            assert item_row[1] is None, f"Expected lease_owner None, got {item_row[1]}"
            assert item_row[2] >= 1, f"Expected fencing_token incremented, got {item_row[2]}"
            assert item_row[3] >= 2, f"Expected attempt_number incremented, got {item_row[3]}"
            conn.close()

            # 4. Run new worker on restored server to ensure the item can be claimed and reaches terminal success
            proc_res = run_worker(
                data_dir=restored_data_dir,
                server_url=server.base_url,
                profile_path=profile_path,
                worker_id="worker-restored-bk01",
                max_items=1
            )
            assert proc_res.returncode == 0, f"Worker failed with code {proc_res.returncode}:\nSTDOUT: {proc_res.stdout}\nSTDERR: {proc_res.stderr}"

            conn = sqlite3.connect(str(restored_data_dir / "app.db"))
            cur = conn.cursor()
            cur.execute("SELECT status FROM generation_item WHERE job_id = ?", (job_id,))
            row = cur.fetchone()
            assert row is not None and row[0] == "completed", f"Expected item status completed, got {row}\nWorker stdout: {proc_res.stdout}\nWorker stderr: {proc_res.stderr}"
            cur.execute("SELECT id FROM question_candidate WHERE job_id = ? AND disposition = 'published'", (job_id,))
            assert cur.fetchone() is not None, "Expected published question candidate"
            conn.close()

    finally:
        provider.stop()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "backup_dir": str(backup_dir),
            "restored_data_dir": str(restored_data_dir),
            "practice_session_id": practice_session_id,
            "exam_id": exam_id,
            "job_id": job_id
        }
    }
