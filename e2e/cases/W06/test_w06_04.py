"""W06-04: 预算拒绝和失败状态"""
import json
from pathlib import Path
import sqlite3
import time
import uuid
import httpx
from playwright.sync_api import expect
from support.web_harness import WebHarness


def run(context: dict) -> dict:
    with WebHarness(context, seed="core") as harness:
        base_url = harness.base_url
        page = harness.page
        data_dir = harness.data_dir
        db_path = data_dir / "app.db"

        # 1. Get bank and sense
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
            LIMIT 1
        """, (bank_id,))
        sense_id, sense_ver = cur.fetchone()
        conn.close()

        # 2. Create job
        client = httpx.Client(base_url=base_url)
        csrf_resp = client.get("/client-context")
        csrf_token = csrf_resp.json().get("csrf_token", "")

        job_resp = client.post(
            f"/api/v1/banks/{bank_id}/generation-jobs",
            headers={"X-CSRF-Token": csrf_token, "Idempotency-Key": str(uuid.uuid4())},
            json={
                "sense_refs": [{"sense_id": sense_id, "sense_version_id": sense_ver}],
                "generation_config_version": "v1",
                "quality_policy_version": "v1",
                "expected_revision": bank_rev,
            },
        )
        assert job_resp.status_code == 202
        job_id = job_resp.json()["job_id"]

        # 3. Trigger budget exhaustion in DB
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("""
            UPDATE generation_budget_ledger
            SET authorized_requests = max_requests,
                total_tokens = max_tokens + 500,
                budget_overrun = 1
            WHERE job_id = ?
        """, (job_id,))
        cur.execute("UPDATE generation_job SET status = 'budget_exhausted' WHERE id = ?", (job_id,))

        now_micro = int(time.time() * 1_000_000)
        cur.execute("""
            UPDATE generation_item
            SET status = 'claimed',
                lease_owner = 'worker-budget-test',
                lease_until = ?,
                fencing_token = 1
            WHERE job_id = ?
        """, (now_micro + 60_000_000, job_id))
        conn.commit()

        # Verify worker reservation endpoint rejects with BUDGET_EXHAUSTED
        token_path = data_dir / "worker.token"
        assert token_path.is_file()
        worker_token = token_path.read_text(encoding="utf-8").strip()

        cur.execute("SELECT id, fencing_token, input_digest FROM generation_item WHERE job_id = ?", (job_id,))
        item_row = cur.fetchone()
        assert item_row is not None
        item_id, f_tok, inp_dig = item_row
        conn.close()

        res_call = client.post(
            "/internal/v1/generation/calls/reserve",
            headers={"Authorization": f"Bearer {worker_token}", "Idempotency-Key": str(uuid.uuid4())},
            json={
                "worker_id": "worker-budget-test",
                "item_id": item_id,
                "fencing_token": f_tok,
                "input_digest": inp_dig,
                "logical_call_key": {"attempt_number": 1, "stage": "generator", "candidate_index": 0, "evaluation_index": 0},
                "network_attempt_id": str(uuid.uuid4()),
                "profile_version": "v1",
                "input_sha256": "digest",
                "input_token_estimate": 100,
                "max_output_tokens": 100,
            },
        )
        assert res_call.status_code == 409, f"Expected 409 BUDGET_EXHAUSTED, got {res_call.status_code}: {res_call.text}"
        assert res_call.json().get("error", {}).get("code") == "BUDGET_EXHAUSTED"

        # 4. In Browser UI, check budget exhausted badge and overrun alert
        page.goto(f"{base_url}/generation", wait_until="networkidle")
        expect(page.locator("[data-testid='generation-view']")).to_be_visible()

        # Status badge shows budget_exhausted
        expect(page.locator("[data-testid='badge-status-budget_exhausted']")).to_be_visible()
        expect(page.locator("[data-testid='alert-overrun']")).to_be_visible()

        # Open details
        page.locator(f"[data-testid='view-job-btn-{job_id}']").click()
        expect(page.locator("[data-testid='job-detail-drawer']")).to_be_visible()
        expect(page.locator("[data-testid='job-detail-drawer'] [data-testid='alert-overrun']")).to_be_visible()

        # Check no credentials leaked in DOM text
        body_text = page.locator("body").inner_text()
        assert worker_token not in body_text, "Worker bearer token must never be exposed in UI"
        assert "api_key" not in body_text.lower(), "API key references must not be exposed"

        return {
            "status": "passed",
            "details": {
                "budget_exhausted_status_verified": True,
                "reserve_call_budget_exhausted_409_verified": True,
                "budget_overrun_alert_rendered": True,
                "no_credentials_leaked_verified": True,
            },
        }
