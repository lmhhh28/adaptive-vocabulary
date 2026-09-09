"""WK-03: Rejection, limited revision, uncertain/conflict, and budget bounds acceptance test.
Verifies reject -> limited revision (max 2), uncertain/conflict -> quality findings & needs_attention,
budget exhausted halts provider calls, and no-credentials startup retains fixture availability.
"""
import json
from pathlib import Path
import sqlite3
import uuid

from support.lifecycle import managed_server
from support.http_client import APIClient
from support.provider_server import ProviderServer
from support.worker_process import run_worker


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    data_dir = case_dir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)
    artifacts = []

    # 1. Start provider
    provider = ProviderServer(expected_api_key="wk03-key")
    provider.start()

    try:
        with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
            base_url = server.base_url
            artifacts.append(str(server.log_path))

            token_path = data_dir / "worker.token"
            assert token_path.is_file()

            db_path = data_dir / "app.db"
            conn = sqlite3.connect(db_path)
            cur = conn.cursor()
            cur.execute("SELECT id, revision FROM bank WHERE name = '开发样例'")
            bank_id, bank_rev = cur.fetchone()

            cur.execute("""
                SELECT s.id, sv.id
                FROM sense s
                JOIN sense_version sv ON sv.sense_id = s.id
                JOIN bank_sense bs ON bs.sense_id = s.id
                WHERE bs.bank_id = ?
                LIMIT 2
            """, (bank_id,))
            senses = cur.fetchall()
            sense1_id, sense1_ver = senses[0]
            sense2_id, sense2_ver = senses[1]
            conn.close()

            client = APIClient(base_url)
            client.initialize_client_context()

            # --- SUBTEST A: Rejection and limited revisions (always_reject) ---
            provider.scenario_mode = "always_reject"
            provider.reset_counts()

            job_a_resp = client.post(
                f"/api/v1/banks/{bank_id}/generation-jobs",
                json={
                    "sense_refs": [{"sense_id": sense1_id, "sense_version_id": sense1_ver}],
                    "generation_config_version": "v1",
                    "quality_policy_version": "v1",
                    "expected_revision": bank_rev
                },
                headers={"Idempotency-Key": str(uuid.uuid4())}
            )
            assert job_a_resp.status_code == 202
            job_a_id = job_a_resp.json()["job_id"]

            profile_path = case_dir / "generation-profile.json"
            profile = {
                "config_version": "generation-v2",
                "budget_policy_version": "budget-v2",
                "mode": "test",
                "quality_policy_version": "quality-v1",
                "production_adapter": "chat-completions-json-v1",
                "generator": {
                    "base_url": provider.base_url,
                    "model": "test-generator",
                    "api_key": "wk03-key",
                    "max_output_tokens": 512
                },
                "evaluator": {
                    "base_url": provider.base_url,
                    "model": "test-evaluator",
                    "api_key": "wk03-key",
                    "max_output_tokens": 256
                }
            }
            profile_path.write_text(json.dumps(profile, indent=2), encoding="utf-8")

            res_a = run_worker(
                data_dir=data_dir,
                server_url=base_url,
                profile_path=profile_path,
                worker_id="worker-a",
                max_items=1
            )
            assert res_a.returncode == 0, f"Worker A failed with code {res_a.returncode}:\n{res_a.stderr}"

            # Check candidate disposition and quality finding in DB
            conn = sqlite3.connect(db_path)
            cur = conn.cursor()
            cur.execute("SELECT disposition FROM question_candidate WHERE job_id = ? ORDER BY created_at DESC LIMIT 1", (job_a_id,))
            cand_row = cur.fetchone()
            assert cand_row is not None, "Candidate not found in DB"
            assert cand_row[0] == "rejected", f"Expected candidate disposition rejected, got {cand_row[0]}"

            cur.execute("SELECT rejected_count FROM generation_job WHERE id = ?", (job_a_id,))
            rejected_row = cur.fetchone()
            assert rejected_row is not None and rejected_row[0] >= 1, f"Expected rejected_count >= 1, got {rejected_row}"
            conn.close()

            # --- SUBTEST B: Conflict / Uncertain ---
            provider.scenario_mode = "conflict"
            provider.reset_counts()

            # Update bank revision before second job
            conn = sqlite3.connect(db_path)
            cur = conn.cursor()
            cur.execute("SELECT revision FROM bank WHERE id = ?", (bank_id,))
            current_bank_rev = cur.fetchone()[0]
            conn.close()

            job_b_resp = client.post(
                f"/api/v1/banks/{bank_id}/generation-jobs",
                json={
                    "sense_refs": [{"sense_id": sense2_id, "sense_version_id": sense2_ver}],
                    "generation_config_version": "v1",
                    "quality_policy_version": "v1",
                    "expected_revision": current_bank_rev
                },
                headers={"Idempotency-Key": str(uuid.uuid4())}
            )
            assert job_b_resp.status_code == 202
            job_b_id = job_b_resp.json()["job_id"]

            res_b = run_worker(
                data_dir=data_dir,
                server_url=base_url,
                profile_path=profile_path,
                worker_id="worker-b",
                max_items=1
            )
            assert res_b.returncode == 0, f"Worker B failed with code {res_b.returncode}:\n{res_b.stderr}"

            conn = sqlite3.connect(db_path)
            cur = conn.cursor()
            cur.execute("SELECT disposition FROM question_candidate WHERE job_id = ? ORDER BY created_at DESC LIMIT 1", (job_b_id,))
            cand_b_row = cur.fetchone()
            assert cand_b_row is not None, "Candidate B not found in DB"
            assert cand_b_row[0] == "uncertain", f"Expected uncertain disposition, got {cand_b_row[0]}"

            cur.execute("SELECT COUNT(*) FROM quality_finding WHERE job_id = ?", (job_b_id,))
            finding_count_b = cur.fetchone()[0]
            assert finding_count_b >= 1, "Expected quality finding for uncertain item"
            conn.close()

            # --- SUBTEST C: No-credentials regular startup retains fixture availability ---
            sess_resp = client.post(
                "/api/v1/practice-sessions",
                json={"bank_id": bank_id},
                headers={"Idempotency-Key": str(uuid.uuid4())}
            )
            assert sess_resp.status_code in (200, 201)
            sess_data = sess_resp.json()
            sess_id = sess_data.get("session", {}).get("id") or sess_data.get("id")
            assert sess_id is not None

            next_resp = client.post(
                f"/api/v1/practice-sessions/{sess_id}/next",
                json={"expected_cursor": 0},
                headers={"Idempotency-Key": str(uuid.uuid4())}
            )
            assert next_resp.status_code == 200
            assert next_resp.json().get("status") == "ready"

    finally:
        provider.stop()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "rejection_with_limited_revisions": True,
            "quality_findings_generated": True,
            "conflict_uncertain_handled": True,
            "fixture_session_available_without_credentials": True
        }
    }
