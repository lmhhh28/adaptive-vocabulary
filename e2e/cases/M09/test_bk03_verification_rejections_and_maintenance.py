"""BK-03: Verification rejections, active target refusal, and maintenance preservation acceptance test.
Verifies:
1. Tampered checksum rejects verify with non-zero exit code.
2. Path traversal in manifest rejects verify with non-zero exit code.
3. Unknown format_version or future schema_version rejects verify with non-zero exit code.
4. Attempting restore into an active directory with running server fails with exit code 2 (SERVICE_RUNNING).
5. Backing up a database in maintenance mode and restoring preserves rebuilding mode:
   new server blocks writes with 503 MAINTENANCE until rebuild is successfully restarted.
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
    src_data_dir = case_dir / "src_data"
    src_data_dir.mkdir(parents=True, exist_ok=True)
    valid_backup_dir = case_dir / "valid_backup"
    artifacts = []

    bank_id = "00000000-0000-4000-8000-000000000801"

    # Step 1: Create a valid seeded database and backup
    with managed_server(bin_path, src_data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))

    res_create = subprocess.run([
        str(bin_path), "backup", "create",
        "--data-dir", str(src_data_dir),
        "--output", str(valid_backup_dir)
    ], capture_output=True, text=True)
    assert res_create.returncode == 0, f"Initial backup create failed: {res_create.stderr}"

    # Step 2: Verification Rejections
    # 2.1 Tampered checksum
    corrupt_dir = case_dir / "corrupt_backup"
    subprocess.run(["cp", "-r", str(valid_backup_dir), str(corrupt_dir)], check=True)
    app_db = corrupt_dir / "app.db"
    with open(app_db, "r+b") as f:
        f.seek(100)
        b = f.read(1)
        f.seek(100)
        f.write(bytes([b[0] ^ 0xFF]))

    res_v1 = subprocess.run([str(bin_path), "backup", "verify", "--input", str(corrupt_dir)], capture_output=True, text=True)
    assert res_v1.returncode != 0, "Expected non-zero exit on tampered db"
    v1_out = res_v1.stdout + res_v1.stderr
    assert "checksum_mismatch" in v1_out or "failed" in v1_out

    # 2.2 Path traversal in manifest
    traversal_dir = case_dir / "traversal_backup"
    subprocess.run(["cp", "-r", str(valid_backup_dir), str(traversal_dir)], check=True)
    m_path = traversal_dir / "manifest.json"
    m_data = json.loads(m_path.read_text())
    m_data["files"].append({
        "relative_path": "../../etc/passwd",
        "role": "database",
        "bytes": 50,
        "sha256": "abcdef"
    })
    m_path.write_text(json.dumps(m_data, indent=2))

    res_v2 = subprocess.run([str(bin_path), "backup", "verify", "--input", str(traversal_dir)], capture_output=True, text=True)
    assert res_v2.returncode != 0, "Expected non-zero exit on path traversal manifest"
    v2_out = res_v2.stdout + res_v2.stderr
    assert "path traversal" in v2_out or "failed" in v2_out

    # 2.3 Unknown format_version
    bad_fmt_dir = case_dir / "bad_fmt_backup"
    subprocess.run(["cp", "-r", str(valid_backup_dir), str(bad_fmt_dir)], check=True)
    m_path = bad_fmt_dir / "manifest.json"
    m_data = json.loads(m_path.read_text())
    m_data["format_version"] = "99.0.0"
    m_path.write_text(json.dumps(m_data, indent=2))

    res_v3 = subprocess.run([str(bin_path), "backup", "verify", "--input", str(bad_fmt_dir)], capture_output=True, text=True)
    assert res_v3.returncode != 0, "Expected non-zero exit on unknown format_version"

    # 2.4 Unknown future schema_version
    bad_schema_dir = case_dir / "bad_schema_backup"
    subprocess.run(["cp", "-r", str(valid_backup_dir), str(bad_schema_dir)], check=True)
    m_path = bad_schema_dir / "manifest.json"
    m_data = json.loads(m_path.read_text())
    m_data["schema_version"] = 9999
    m_path.write_text(json.dumps(m_data, indent=2))

    res_v4 = subprocess.run([str(bin_path), "backup", "verify", "--input", str(bad_schema_dir)], capture_output=True, text=True)
    assert res_v4.returncode != 0, "Expected non-zero exit on future schema_version"

    # Step 3: Active target directory refusal on restore
    active_target_dir = case_dir / "active_target"
    active_target_dir.mkdir(parents=True, exist_ok=True)
    with managed_server(bin_path, active_target_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))
        # Attempt to restore over the actively running directory
        res_active = subprocess.run([
            str(bin_path), "backup", "restore",
            "--input", str(valid_backup_dir),
            "--target", str(active_target_dir)
        ], capture_output=True, text=True)
        assert res_active.returncode == 2, f"Expected returncode 2 on active target, got {res_active.returncode}: {res_active.stdout} {res_active.stderr}"
        assert "SERVICE_RUNNING" in res_active.stdout or "active" in res_active.stdout

    # Step 4: Maintenance state preservation on backup & restore
    maint_data_dir = case_dir / "maint_src_data"
    maint_data_dir.mkdir(parents=True, exist_ok=True)
    maint_backup_dir = case_dir / "maint_backup"
    maint_restored_dir = case_dir / "maint_restored"

    run_id = str(uuid.uuid4())
    # Initialize maint db
    with managed_server(bin_path, maint_data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))
        # Trigger an evidence invalidation to enter maintenance mode
        client = APIClient(server.base_url)
        client.initialize_client_context()

        conn = sqlite3.connect(str(maint_data_dir / "app.db"))
        cur = conn.cursor()
        cur.execute("SELECT id, revision FROM question_version WHERE status = 'published' LIMIT 1")
        qv_id, qv_rev = cur.fetchone()
        conn.close()

        # Invalidate question version
        inv_resp = client.post(f"/api/v1/question-versions/{qv_id}/evidence-dispositions", json={
            "action": "invalidate",
            "reason": "Test maintenance preservation",
            "expected_revision": qv_rev
        }, headers={"Idempotency-Key": str(uuid.uuid4())})
        assert inv_resp.status_code == 200

        # Now simulate an abort on evidence_revision so status becomes aborted_maintenance and mode stays rebuilding
        conn = sqlite3.connect(str(maint_data_dir / "app.db"))
        cur = conn.cursor()
        now_micro = int(time.time() * 1000000)
        cur.execute("""
            INSERT INTO model_rebuild_run (
                id, kind, base_active_version, target_projection_version, target_policy,
                base_revision_digest, fixed_watermark, status, requires_rebuild, created_at, updated_at
            ) VALUES (?, 'evidence_revision', 'v1', 'v_test_target', 'policy-v2', 'digest-1', 1, 'failed', 1, ?, ?)
        """, (run_id, now_micro, now_micro))
        cur.execute("UPDATE projection_control SET mode = 'rebuilding', run_id = ?, run_status = 'failed', requires_rebuild = 1, updated_at = ? WHERE id = 1", (run_id, now_micro))
        conn.commit()
        conn.close()

    # Abort the rebuild run while server is stopped
    res_abort = subprocess.run([
        str(bin_path), "learning", "rebuild", "abort",
        "--run", run_id,
        "--data-dir", str(maint_data_dir)
    ], capture_output=True, text=True)
    assert res_abort.returncode == 0, f"abort failed: {res_abort.stderr}"
    abort_out = json.loads(res_abort.stdout)
    assert abort_out["status"] == "aborted_maintenance"
    assert abort_out["requires_rebuild"] is True
    assert abort_out["next_action"] == "restart"

    # Create backup while in maintenance
    res_mbk = subprocess.run([
        str(bin_path), "backup", "create",
        "--data-dir", str(maint_data_dir),
        "--output", str(maint_backup_dir)
    ], capture_output=True, text=True)
    assert res_mbk.returncode == 0

    # Restore to new directory
    res_mrest = subprocess.run([
        str(bin_path), "backup", "restore",
        "--input", str(maint_backup_dir),
        "--target", str(maint_restored_dir)
    ], capture_output=True, text=True)
    assert res_mrest.returncode == 0

    # Start server on restored directory -> must still be in maintenance gate!
    with managed_server(bin_path, maint_restored_dir, listen_port=0, log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))
        r_client = APIClient(server.base_url)
        r_client.initialize_client_context()

        # Write request must be blocked by MaintenanceGateMiddleware with 503
        w_resp = r_client.post(
            "/api/v1/practice-sessions",
            json={"bank_id": bank_id},
            headers={"Idempotency-Key": str(uuid.uuid4())}
        )
        assert w_resp.status_code == 503, f"Expected 503 MAINTENANCE on restored server, got {w_resp.status_code}: {w_resp.text}"
        assert "MAINTENANCE" in w_resp.text

    # Rebuild restart while server is stopped
    res_restart = subprocess.run([
        str(bin_path), "learning", "rebuild", "restart",
        "--run", run_id,
        "--projection", "v_restored_new",
        "--data-dir", str(maint_restored_dir)
    ], capture_output=True, text=True)
    assert res_restart.returncode == 0, f"restart failed: {res_restart.stderr}"
    restart_out = json.loads(res_restart.stdout)
    assert restart_out["status"] == "succeeded"
    assert restart_out["active_version"] == "v_restored_new"

    # Start server again -> maintenance gate released, writes succeed!
    with managed_server(bin_path, maint_restored_dir, listen_port=0, log_dir=case_dir) as server:
        artifacts.append(str(server.log_path))
        r_client = APIClient(server.base_url)
        r_client.initialize_client_context()

        w_resp2 = r_client.post(
            "/api/v1/practice-sessions",
            json={"bank_id": bank_id},
            headers={"Idempotency-Key": str(uuid.uuid4())}
        )
        assert w_resp2.status_code == 201, f"Expected 201 after rebuild restart, got {w_resp2.status_code}: {w_resp2.text}"

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "valid_backup_dir": str(valid_backup_dir),
            "maint_restored_dir": str(maint_restored_dir)
        }
    }
