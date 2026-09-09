"""RB-02: Rebuild failure, abort maintenance, restart and policy_only abort acceptance test.
Verifies:
1. Invalidate failure -> abort results in aborted_maintenance (still in maintenance, requires_rebuild=True, next_action=restart).
2. Server restart keeps maintenance gate: write APIs return 503 MAINTENANCE.
3. Process lock protects against running CLI while server is running (SERVICE_RUNNING, exit code 2).
4. CLI restart succeeds: switches active projection version, unlocks maintenance gate.
5. policy_only rebuild abort with unchanged digest safely restores base active projection and unlocks gate.
"""
import json
from pathlib import Path
import sqlite3
import subprocess
import time
import uuid
from support.lifecycle import managed_server
from support.http_client import APIClient


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    data_dir = case_dir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)
    artifacts = []
    db_path = data_dir / "app.db"
    policy_path = context["policy_path"]

    bank_id = "00000000-0000-4000-8000-000000000801"

    # Step 1: Start server to seed database
    with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))
        base_url = server.base_url

        client = APIClient(base_url)
        client.initialize_client_context()

        # Invalidate a question version
        conn = sqlite3.connect(str(db_path))
        cur = conn.cursor()
        cur.execute("SELECT id, revision FROM question_version WHERE status = 'published' LIMIT 1")
        row = cur.fetchone()
        assert row is not None
        qv_id, qv_rev = row[0], row[1]
        conn.close()

        disp_resp = client.post(f"/api/v1/question-versions/{qv_id}/evidence-dispositions", json={
            "action": "invalidate",
            "reason": "Test invalidate for abort flow",
            "expected_revision": qv_rev,
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert disp_resp.status_code == 200, f"Invalidate failed: {disp_resp.status_code}: {disp_resp.text}"

    # Server stopped.
    # Step 2: In SQLite, simulate a failed evidence_revision rebuild run
    now = int(time.time() * 1000000)
    failed_run_id = "run-ev-failed-" + uuid.uuid4().hex[:8]
    conn = sqlite3.connect(str(db_path))
    cur = conn.cursor()
    cur.execute("SELECT active_version FROM projection_control WHERE id = 1")
    base_ver = cur.fetchone()[0]

    cur.execute("""
        INSERT INTO model_rebuild_run (
            id, kind, base_active_version, target_projection_version, target_policy,
            base_revision_digest, fixed_watermark, status, requires_rebuild,
            created_at, updated_at
        ) VALUES (?, 'evidence_revision', ?, 'v_failed', 'policy-v2', 'digest-dummy', 10, 'failed', 1, ?, ?)
    """, (failed_run_id, base_ver, now, now))

    cur.execute("""
        UPDATE projection_control
        SET mode = 'rebuilding', run_id = ?, run_status = 'failed', requires_rebuild = 1, updated_at = ?
        WHERE id = 1
    """, (failed_run_id, now))
    conn.commit()
    conn.close()

    # Step 3: Run CLI rebuild abort
    cmd_abort = [
        str(bin_path), "learning", "rebuild", "abort",
        "--run", failed_run_id,
        "--data-dir", str(data_dir),
        "--policy", str(policy_path),
    ]
    proc = subprocess.run(cmd_abort, capture_output=True, text=True)
    assert proc.returncode == 0, f"CLI abort failed with code {proc.returncode}: stderr={proc.stderr}, stdout={proc.stdout}"
    abort_out = json.loads(proc.stdout.strip())

    assert abort_out["run_id"] == failed_run_id
    assert abort_out["status"] == "aborted_maintenance", f"Expected aborted_maintenance, got {abort_out['status']}"
    assert abort_out["mode"] == "rebuilding", f"Expected mode rebuilding, got {abort_out['mode']}"
    assert abort_out["requires_rebuild"] is True, "Expected requires_rebuild=True"
    assert abort_out["next_action"] == "restart", f"Expected next_action=restart, got {abort_out['next_action']}"

    # Verify in DB
    conn = sqlite3.connect(str(db_path))
    cur = conn.cursor()
    cur.execute("SELECT mode, run_status, requires_rebuild FROM projection_control WHERE id = 1")
    pc_mode, pc_run_status, pc_req_reb = cur.fetchone()
    assert pc_mode == "rebuilding"
    assert pc_run_status == "aborted_maintenance"
    assert pc_req_reb == 1
    conn.close()

    # Step 4: Restart server in maintenance state
    with managed_server(bin_path, data_dir, listen_port=0, seed="", log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))
        base_url = server.base_url
        client = APIClient(base_url)
        client.initialize_client_context()

        # Check /api/v1/maintenance endpoint
        maint_resp = client.get("/api/v1/maintenance")
        assert maint_resp.status_code == 200
        maint_data = maint_resp.json()
        assert maint_data["mode"] == "rebuilding"
        assert maint_data["requires_rebuild"] is True
        assert maint_data["run_status"] == "aborted_maintenance"
        assert maint_data["next_action"] == "restart"

        # Verify that write APIs are blocked by maintenance gate (503 MAINTENANCE)
        write_resp = client.post("/api/v1/practice-sessions", json={"bank_id": bank_id}, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert write_resp.status_code == 503, f"Expected 503 MAINTENANCE, got {write_resp.status_code}: {write_resp.text}"
        assert write_resp.json()["error"]["code"] == "MAINTENANCE"

        # Attempting CLI restart while server is running must fail with SERVICE_RUNNING (exit code 2)
        cmd_restart_running = [
            str(bin_path), "learning", "rebuild", "restart",
            "--run", failed_run_id,
            "--projection", "v_restart_test",
            "--data-dir", str(data_dir),
            "--policy", str(policy_path),
        ]
        proc_lock = subprocess.run(cmd_restart_running, capture_output=True, text=True)
        assert proc_lock.returncode == 2, f"Expected exit code 2 (SERVICE_RUNNING), got {proc_lock.returncode}: {proc_lock.stdout} {proc_lock.stderr}"
        lock_out = json.loads(proc_lock.stdout.strip())
        assert lock_out.get("code") == "SERVICE_RUNNING"

    # Server stopped.
    # Step 5: Execute CLI restart
    target_proj = "v_restart_success"
    cmd_restart = [
        str(bin_path), "learning", "rebuild", "restart",
        "--run", failed_run_id,
        "--projection", target_proj,
        "--data-dir", str(data_dir),
        "--policy", str(policy_path),
    ]
    proc_restart = subprocess.run(cmd_restart, capture_output=True, text=True)
    assert proc_restart.returncode == 0, f"Restart failed with code {proc_restart.returncode}: {proc_restart.stderr} {proc_restart.stdout}"
    restart_out = json.loads(proc_restart.stdout.strip())

    assert restart_out["status"] == "succeeded"
    assert restart_out["mode"] == "active"
    assert restart_out["active_version"] == target_proj
    assert restart_out["requires_rebuild"] is False
    assert restart_out["next_action"] == "none"

    # Step 6: Start server again, verify write APIs succeed and gate is unlocked
    with managed_server(bin_path, data_dir, listen_port=0, seed="", log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))
        base_url = server.base_url
        client = APIClient(base_url)
        client.initialize_client_context()

        maint_resp2 = client.get("/api/v1/maintenance")
        assert maint_resp2.status_code == 200
        assert maint_resp2.json()["mode"] == "active"

        write_resp2 = client.post("/api/v1/practice-sessions", json={"bank_id": bank_id}, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert write_resp2.status_code == 201, f"Expected 201 created, got {write_resp2.status_code}: {write_resp2.text}"

    # Server stopped.
    # Step 7: Test policy_only abort (safely unlocks gate when digest is unchanged)
    conn = sqlite3.connect(str(db_path))
    cur = conn.cursor()
    # Compute current digest directly
    cur.execute("SELECT id, event_id, event_type, revision_kind, reason, created_at FROM event_revision ORDER BY id ASC")
    import hashlib
    h = hashlib.sha256()
    for r in cur.fetchall():
        row_str = f"{r[0]}|{r[1]}|{r[2]}|{r[3]}|{r[4]}|{r[5]}\n"
        h.update(row_str.encode("utf-8"))
    cur_digest = h.hexdigest()

    policy_run_id = "run-policy-" + uuid.uuid4().hex[:8]
    cur.execute("""
        INSERT INTO model_rebuild_run (
            id, kind, base_active_version, target_projection_version, target_policy,
            base_revision_digest, fixed_watermark, status, requires_rebuild,
            created_at, updated_at
        ) VALUES (?, 'policy_only', ?, 'v_policy_temp', 'policy-v2', ?, 10, 'running', 1, ?, ?)
    """, (policy_run_id, target_proj, cur_digest, now, now))

    cur.execute("""
        UPDATE projection_control
        SET mode = 'rebuilding', run_id = ?, run_status = 'running', requires_rebuild = 1, updated_at = ?
        WHERE id = 1
    """, (policy_run_id, now))
    conn.commit()
    conn.close()

    cmd_policy_abort = [
        str(bin_path), "learning", "rebuild", "abort",
        "--run", policy_run_id,
        "--data-dir", str(data_dir),
        "--policy", str(policy_path),
    ]
    proc_pa = subprocess.run(cmd_policy_abort, capture_output=True, text=True)
    assert proc_pa.returncode == 0, f"policy_only abort failed: {proc_pa.stderr} {proc_pa.stdout}"
    pa_out = json.loads(proc_pa.stdout.strip())

    assert pa_out["status"] == "aborted"
    assert pa_out["mode"] == "active"
    assert pa_out["active_version"] == target_proj
    assert pa_out["requires_rebuild"] is False
    assert pa_out["next_action"] == "none"

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "failed_run_id": failed_run_id,
            "target_proj": target_proj,
            "policy_run_id": policy_run_id,
        },
    }
