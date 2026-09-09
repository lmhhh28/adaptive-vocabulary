"""WK-01: Generation pipeline and budget ledger acceptance test.
Verifies fixed job -> real provider requests -> 2 independent evaluations -> Go publishing -> question usable in practice session, with budget ledger fully traceable.
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

    # 1. Start test provider server on loopback
    provider = ProviderServer(expected_api_key="wk01-key", scenario_mode="normal_pass")
    provider.start()

    try:
        # 2. Start Go server
        with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
            base_url = server.base_url
            artifacts.append(str(server.log_path))

            # 3. Read worker token
            token_path = data_dir / "worker.token"
            assert token_path.is_file(), "worker.token missing"

            # 4. Get bank and sense from DB
            db_path = data_dir / "app.db"
            conn = sqlite3.connect(db_path)
            cur = conn.cursor()
            cur.execute("SELECT id, revision FROM bank WHERE name = '开发样例'")
            bank_row = cur.fetchone()
            assert bank_row is not None, "Bank not found"
            bank_id, bank_rev = bank_row[0], bank_row[1]

            cur.execute("""
                SELECT s.id, sv.id
                FROM sense s
                JOIN sense_version sv ON sv.sense_id = s.id
                JOIN bank_sense bs ON bs.sense_id = s.id
                WHERE bs.bank_id = ?
                LIMIT 1
            """, (bank_id,))
            sense_row = cur.fetchone()
            assert sense_row is not None, "Sense not found"
            sense_id, sense_version_id = sense_row[0], sense_row[1]
            conn.close()

            # 5. Create generation job via HTTP
            client = APIClient(base_url)
            client.initialize_client_context()
            job_resp = client.post(
                f"/api/v1/banks/{bank_id}/generation-jobs",
                json={
                    "sense_refs": [{"sense_id": sense_id, "sense_version_id": sense_version_id}],
                    "generation_config_version": "v1",
                    "quality_policy_version": "v1",
                    "expected_revision": bank_rev
                },
                headers={"Idempotency-Key": str(uuid.uuid4())}
            )
            assert job_resp.status_code == 202, f"Expected 202, got {job_resp.status_code}: {job_resp.text}"
            job_id = job_resp.json()["job_id"]

            # 6. Setup Worker profile pointing to provider
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
                    "api_key": "wk01-key",
                    "max_output_tokens": 1024
                },
                "evaluator": {
                    "base_url": provider.base_url,
                    "model": "test-evaluator",
                    "api_key": "wk01-key",
                    "max_output_tokens": 512
                }
            }
            profile_path.write_text(json.dumps(profile, indent=2), encoding="utf-8")

            # 7. Run real Worker process
            proc_res = run_worker(
                data_dir=data_dir,
                server_url=base_url,
                profile_path=profile_path,
                worker_id="worker-wk01",
                max_items=1
            )
            assert proc_res.returncode == 0, f"Worker process failed with code {proc_res.returncode}:\nSTDOUT: {proc_res.stdout}\nSTDERR: {proc_res.stderr}"

            # Verify published candidate in SQLite
            conn = sqlite3.connect(db_path)
            cur = conn.cursor()
            cur.execute("SELECT id, published_version_id FROM question_candidate WHERE job_id = ? AND disposition = 'published'", (job_id,))
            cand_row = cur.fetchone()
            assert cand_row is not None, "Published candidate not found in database"
            cand_id, published_version_id = cand_row
            assert published_version_id is not None, "Missing published_version_id"

            # 8. Verify published content can be served in practice session
            sess_resp = client.post(
                "/api/v1/practice-sessions",
                json={"bank_id": bank_id},
                headers={"Idempotency-Key": str(uuid.uuid4())}
            )
            assert sess_resp.status_code in (200, 201), f"Practice session create failed: {sess_resp.text}"
            sess_data = sess_resp.json()
            sess_id = sess_data.get("session", {}).get("id") or sess_data.get("id")
            assert sess_id is not None, f"Failed to obtain session ID from {sess_data}"

            next_resp = client.post(
                f"/api/v1/practice-sessions/{sess_id}/next",
                json={"expected_cursor": 0},
                headers={"Idempotency-Key": str(uuid.uuid4())}
            )
            assert next_resp.status_code == 200, f"Next question failed: {next_resp.text}"
            next_data = next_resp.json()
            assert next_data.get("status") == "ready", f"Expected ready question: {next_data}"
            pub_q = next_data["question"]
            assert len(pub_q["options"]) == 4, f"Expected 4 options, got {len(pub_q['options'])}"

            # 9. Verify budget ledger and call records in SQLite
            cur.execute("""
                SELECT authorized_requests, total_tokens, budget_overrun
                FROM generation_budget_ledger
                WHERE job_id = ?
            """, (job_id,))
            ledger_row = cur.fetchone()
            assert ledger_row is not None, "Ledger row not found"
            auth_reqs, total_toks, overrun = ledger_row
            assert auth_reqs == 3, f"Expected 3 authorized requests, got {auth_reqs}"
            assert total_toks > 0, f"Expected total_tokens > 0, got {total_toks}"
            assert overrun == 0, "Expected budget_overrun == 0"

            cur.execute("""
                SELECT stage, status, outcome, accounted_tokens, provider_request_id
                FROM generation_call
                WHERE job_id = ?
                ORDER BY created_at ASC
            """, (job_id,))
            calls = cur.fetchall()
            assert len(calls) == 3, f"Expected 3 calls, got {len(calls)}"
            for stage, status, outcome, acc_tok, prov_id in calls:
                assert status == "settled", f"Call not settled: {status}"
                assert outcome == "response", f"Call outcome not response: {outcome}"
                assert acc_tok > 0, f"Accounted tokens <= 0: {acc_tok}"
                assert prov_id is not None, "Provider request id missing"

            conn.close()

            # 10. Verify provider request count
            assert provider.request_count == 3, f"Expected 3 provider requests, got {provider.request_count}"

    finally:
        provider.stop()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "published_version_id": published_version_id,
            "authorized_requests": auth_reqs,
            "total_tokens": total_toks,
            "provider_requests": 3
        }
    }
