"""W06-05: 任务/异常分页及活动会话保护"""
import json
from pathlib import Path
import sqlite3
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

        # -------------------------------------------------------------
        # 1. Create 3 generation jobs for pagination testing
        # -------------------------------------------------------------
        client = httpx.Client(base_url=base_url)
        csrf_resp = client.get("/client-context")
        csrf_token = csrf_resp.json().get("csrf_token", "")

        created_job_ids = []
        for i in range(3):
            cur.execute("SELECT revision FROM bank WHERE id = ?", (bank_id,))
            current_rev = cur.fetchone()[0]
            resp = client.post(
                f"/api/v1/banks/{bank_id}/generation-jobs",
                headers={"X-CSRF-Token": csrf_token, "Idempotency-Key": str(uuid.uuid4())},
                json={
                    "sense_refs": [{"sense_id": sense_id, "sense_version_id": sense_ver}],
                    "generation_config_version": "v1",
                    "quality_policy_version": "v1",
                    "expected_revision": current_rev,
                },
            )
            assert resp.status_code == 202
            created_job_ids.append(resp.json()["job_id"])

        # Insert candidates and 3 quality findings
        cur.execute("SELECT id FROM generation_item WHERE job_id = ?", (created_job_ids[0],))
        item_id = cur.fetchone()[0]

        target_cand_id = "cand-session-lock-test"
        cur.execute("""
            INSERT INTO question_candidate (id, item_id, job_id, attempt_number, candidate_index, content_json, generation_metadata_json, checks_json, disposition, created_at, updated_at)
            VALUES (?, ?, ?, 1, 0, '{"stem":"Test candidate stem for anti-leak boundary","options":[{"text":"Option A","is_correct":true}]}', '{}', '{"result":"pass"}', 'published', 1000, 1000)
        """, (target_cand_id, item_id, created_job_ids[0]))

        for i in range(3):
            cur.execute("""
                INSERT INTO quality_finding (id, job_id, candidate_id, finding_type, details_json, checker_version, status, revision, created_at, updated_at)
                VALUES (?, ?, ?, 'boundary_test', '{}', 'v1', 'open', 1, ?, ?)
            """, (f"finding-w06-{i}", created_job_ids[0], target_cand_id, 2000 + i * 1000, 2000 + i * 1000))

        conn.commit()
        conn.close()

        # -------------------------------------------------------------
        # 2. Test Job Pagination API
        # -------------------------------------------------------------
        j1 = client.get("/api/v1/generation-jobs?limit=2").json()
        assert len(j1["items"]) == 2, f"Expected 2 jobs on page 1, got {len(j1['items'])}"
        assert j1["has_more"] is True
        assert j1["next_cursor"] is not None

        j2 = client.get(f"/api/v1/generation-jobs?cursor={j1['next_cursor']}&limit=2").json()
        assert len(j2["items"]) >= 1, "Expected remaining jobs on page 2"

        # Invalid cursor returns 400
        bad_j = client.get("/api/v1/generation-jobs?cursor=invalid_base64!!!")
        assert bad_j.status_code == 400, f"Expected 400 for bad cursor, got {bad_j.status_code}"

        # -------------------------------------------------------------
        # 3. Test Quality Findings Pagination API
        # -------------------------------------------------------------
        q1 = client.get("/api/v1/quality-findings?limit=2").json()
        assert len(q1["items"]) == 2, f"Expected 2 findings on page 1, got {len(q1['items'])}"
        assert q1["has_more"] is True
        assert q1["next_cursor"] is not None

        q2 = client.get(f"/api/v1/quality-findings?cursor={q1['next_cursor']}&limit=2").json()
        assert len(q2["items"]) >= 1

        bad_q = client.get("/api/v1/quality-findings?cursor=invalid_bad!!!")
        assert bad_q.status_code == 400, f"Expected 400 for bad cursor, got {bad_q.status_code}"

        # -------------------------------------------------------------
        # 4. Test Active Session Protection (Anti-leak boundary)
        # -------------------------------------------------------------
        # Create active practice session
        sess_resp = client.post(
            "/api/v1/practice-sessions",
            headers={"X-CSRF-Token": csrf_token, "Idempotency-Key": str(uuid.uuid4())},
            json={"bank_id": bank_id},
        )
        assert sess_resp.status_code in (200, 201)

        # Direct HTTP query to candidate detail returns 409 SESSION_ACTIVE
        cand_resp = client.get(f"/api/v1/question-candidates/{target_cand_id}")
        assert cand_resp.status_code == 409, f"Expected 409 SESSION_ACTIVE, got {cand_resp.status_code}: {cand_resp.text}"
        assert cand_resp.json().get("error", {}).get("code") == "SESSION_ACTIVE"

        # -------------------------------------------------------------
        # 5. Verify in Browser UI
        # -------------------------------------------------------------
        page.goto(f"{base_url}/generation", wait_until="networkidle")
        expect(page.locator("[data-testid='generation-view']")).to_be_visible()

        # Open job details
        page.locator(f"[data-testid='view-job-btn-{created_job_ids[0]}']").click()
        expect(page.locator("[data-testid='job-detail-drawer']")).to_be_visible()

        # Click inspect candidate during active session -> Expect locked alert!
        page.locator(f"[data-testid='inspect-candidate-btn-{target_cand_id}']").click()
        expect(page.locator("[data-testid='candidate-detail-modal']")).to_be_visible()
        expect(page.locator("[data-testid='session-locked-alert']")).to_be_visible()

        # Close candidate modal
        page.locator(".ant-modal-close").click()
        expect(page.locator("[data-testid='candidate-detail-modal']")).not_to_be_visible()

        # Close session in DB
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        cur.execute("UPDATE session SET status = 'ended' WHERE status = 'open'")
        conn.commit()
        conn.close()

        # Now click inspect candidate again -> Should load successfully!
        page.locator(f"[data-testid='inspect-candidate-btn-{target_cand_id}']").click()
        expect(page.locator("[data-testid='candidate-detail-modal']")).to_be_visible()
        expect(page.locator("[data-testid='candidate-stem']")).to_be_visible()
        expect(page.locator("[data-testid='candidate-stem']")).to_contain_text("anti-leak boundary")

        return {
            "status": "passed",
            "details": {
                "job_pagination_verified": True,
                "finding_pagination_verified": True,
                "invalid_cursor_400_verified": True,
                "active_session_409_boundary_verified": True,
                "ui_session_lock_alert_verified": True,
                "post_session_candidate_access_verified": True,
            },
        }
