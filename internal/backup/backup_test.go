package backup_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"adaptive-vocabulary/internal/backup"
	"adaptive-vocabulary/internal/content"
	"adaptive-vocabulary/internal/platform"
	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	db, err := platform.OpenDB(dir)
	if err != nil {
		t.Fatalf("OpenDB failed: %v", err)
	}
	defer db.Close()

	if _, err := platform.RunMigrations(db.DB); err != nil {
		t.Fatalf("RunMigrations failed: %v", err)
	}

	if err := content.ApplySeed(context.Background(), db.DB, "core"); err != nil {
		t.Fatalf("ApplySeed failed: %v", err)
	}

	var bankID, senseID, senseVerID string
	err = db.QueryRow("SELECT id FROM bank LIMIT 1").Scan(&bankID)
	if err != nil {
		t.Fatalf("query bank: %v", err)
	}
	err = db.QueryRow("SELECT id, sense_id FROM sense_version LIMIT 1").Scan(&senseVerID, &senseID)
	if err != nil {
		t.Fatalf("query sense_version: %v", err)
	}

	// Insert test data
	now := time.Now().UTC().UnixNano() / 1000
	_, err = db.Exec("INSERT INTO global_sequence (seq, event_name, created_at) VALUES (1, 'test_event', ?)", now)
	if err != nil {
		t.Fatalf("insert global_sequence failed: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO generation_job (id, bank_id, generation_config_version, quality_policy_version, status, fixed_input_digest, created_at, updated_at)
		VALUES ('job-1', ?, 'cfg-1', 'pol-1', 'processing', 'dig-job', ?, ?)
	`, bankID, now, now)
	if err != nil {
		t.Fatalf("insert job failed: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO generation_budget_ledger (job_id, max_requests, max_tokens, authorized_requests, total_tokens, budget_overrun, updated_at)
		VALUES ('job-1', 5, 2000, 1, 500, 0, ?)
	`, now)
	if err != nil {
		t.Fatalf("insert ledger failed: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO generation_item (id, job_id, bank_id, sense_id, sense_version_id, fixed_input_json, input_digest, status, lease_owner, lease_until, fencing_token, attempt_number, revision, created_at, updated_at)
		VALUES ('item-1', 'job-1', ?, ?, ?, '{}', 'dig-1', 'claimed', 'worker-1', ?, 1, 1, 1, ?, ?)
	`, bankID, senseID, senseVerID, now+100000000, now, now)
	if err != nil {
		t.Fatalf("insert item failed: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO generation_call (
			reservation_id, job_id, item_id, network_attempt_id, attempt_number,
			stage, candidate_index, evaluation_index, logical_call_key, profile_version,
			input_sha256, input_token_estimate, max_output_tokens, reserved_tokens,
			owner_worker_id, fencing_token, status, outcome, accounted_tokens,
			created_at, updated_at
		) VALUES (
			'res-1', 'job-1', 'item-1', 'net-1', 1,
			'generator', 0, 0, 'item-1:1:generator:0:0', 'prof-1',
			'sha-1', 100, 400, 500,
			'worker-1', 1, 'reserved', NULL, 500,
			?, ?
		)
	`, now, now)
	if err != nil {
		t.Fatalf("insert call failed: %v", err)
	}

	return dir
}

func TestBackupCreateVerifyRestore(t *testing.T) {
	ctx := context.Background()
	srcDir := setupTestDB(t)
	backupDir := filepath.Join(t.TempDir(), "my_backup")
	restoreDir := filepath.Join(t.TempDir(), "restored")

	svc := backup.NewService()

	// 1. Create Backup
	manifest, err := svc.CreateBackup(ctx, srcDir, backupDir, false)
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}
	if manifest.SnapshotWatermark <= 0 {
		t.Errorf("expected watermark > 0, got %d", manifest.SnapshotWatermark)
	}
	if manifest.SchemaVersion != 80 {
		t.Errorf("expected schema_version 80, got %d", manifest.SchemaVersion)
	}
	if len(manifest.Files) != 1 || manifest.Files[0].RelativePath != "app.db" {
		t.Errorf("unexpected files in manifest: %+v", manifest.Files)
	}

	// 2. Verify Backup
	verifyRep, err := svc.VerifyBackup(ctx, backupDir)
	if err != nil {
		t.Fatalf("VerifyBackup failed: %v (errors: %v)", err, verifyRep.Errors)
	}
	if verifyRep.Status != "ok" {
		t.Errorf("expected status ok, got %s", verifyRep.Status)
	}
	if verifyRep.EventWatermark != manifest.SnapshotWatermark {
		t.Errorf("expected watermark %d, got %d", manifest.SnapshotWatermark, verifyRep.EventWatermark)
	}

	// 3. Restore Backup to brand new directory
	restoreRep, err := svc.RestoreBackup(ctx, backupDir, restoreDir)
	if err != nil {
		t.Fatalf("RestoreBackup failed: %v", err)
	}
	if restoreRep.Status != "ok" {
		t.Errorf("expected restore status ok, got %s", restoreRep.Status)
	}
	if restoreRep.Reconciliation.InvalidatedLeasesCount != 1 {
		t.Errorf("expected 1 invalidated lease, got %d", restoreRep.Reconciliation.InvalidatedLeasesCount)
	}
	if restoreRep.Reconciliation.UnknownCallsCount != 1 {
		t.Errorf("expected 1 unknown call, got %d", restoreRep.Reconciliation.UnknownCallsCount)
	}

	// 4. Inspect restored database directly
	restoredDB, err := platform.OpenDB(restoreDir)
	if err != nil {
		t.Fatalf("failed to open restored DB: %v", err)
	}
	defer restoredDB.Close()

	var itemStatus, leaseOwner sql.NullString
	var fence, attempt int64
	err = restoredDB.QueryRow("SELECT status, lease_owner, fencing_token, attempt_number FROM generation_item WHERE id = 'item-1'").
		Scan(&itemStatus, &leaseOwner, &fence, &attempt)
	if err != nil {
		t.Fatalf("query restored item failed: %v", err)
	}
	if itemStatus.String != "pending" {
		t.Errorf("expected item status pending, got %s", itemStatus.String)
	}
	if leaseOwner.Valid {
		t.Errorf("expected lease_owner null, got %s", leaseOwner.String)
	}
	if fence != 2 {
		t.Errorf("expected fencing_token 2, got %d", fence)
	}
	if attempt != 2 {
		t.Errorf("expected attempt_number 2, got %d", attempt)
	}

	var callStatus string
	err = restoredDB.QueryRow("SELECT status FROM generation_call WHERE reservation_id = 'res-1'").Scan(&callStatus)
	if err != nil {
		t.Fatalf("query restored call failed: %v", err)
	}
	if callStatus != "unknown" {
		t.Errorf("expected call status unknown, got %s", callStatus)
	}

	// Budget ledger remains occupied
	var authReqs, totToks int
	err = restoredDB.QueryRow("SELECT authorized_requests, total_tokens FROM generation_budget_ledger WHERE job_id = 'job-1'").
		Scan(&authReqs, &totToks)
	if err != nil {
		t.Fatalf("query budget ledger failed: %v", err)
	}
	if authReqs != 1 || totToks != 500 {
		t.Errorf("expected budget 1 req / 500 toks preserved, got %d / %d", authReqs, totToks)
	}
}

func TestBackupVerificationFailures(t *testing.T) {
	ctx := context.Background()
	srcDir := setupTestDB(t)
	backupDir := filepath.Join(t.TempDir(), "corrupt_backup")

	svc := backup.NewService()
	_, err := svc.CreateBackup(ctx, srcDir, backupDir, false)
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}

	manifestPath := filepath.Join(backupDir, "manifest.json")
	origManifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest failed: %v", err)
	}

	// Case A: Tamper app.db (Checksum mismatch)
	appDBPath := filepath.Join(backupDir, "app.db")
	_ = os.WriteFile(appDBPath, append([]byte("tampered data"), 0), 0600)
	repA, err := svc.VerifyBackup(ctx, backupDir)
	if err == nil || repA.Status != "failed" {
		t.Errorf("expected verify failure on tampered app.db, got nil / status %s", repA.Status)
	}

	// Restore original manifest and app.db for next test
	if err := os.RemoveAll(backupDir); err != nil {
		t.Fatalf("remove corrupted test fixture: %v", err)
	}
	if _, err := svc.CreateBackup(ctx, srcDir, backupDir, false); err != nil {
		t.Fatalf("recreate clean backup fixture: %v", err)
	}

	// Case B: Path traversal in manifest
	var m backup.Manifest
	_ = json.Unmarshal(origManifestBytes, &m)
	m.Files = append(m.Files, backup.BackupFileInfo{
		RelativePath: "../../etc/passwd",
		Role:         "database",
		Bytes:        100,
		SHA256:       "abc",
	})
	mBytes, _ := json.Marshal(m)
	_ = os.WriteFile(manifestPath, mBytes, 0600)

	repB, err := svc.VerifyBackup(ctx, backupDir)
	if err == nil || repB.Status != "failed" {
		t.Errorf("expected verify failure on path traversal, got nil / status %s", repB.Status)
	}

	// Case C: Unsupported format_version
	_ = json.Unmarshal(origManifestBytes, &m)
	m.FormatVersion = "9.9.9"
	mBytes, _ = json.Marshal(m)
	_ = os.WriteFile(manifestPath, mBytes, 0600)

	repC, err := svc.VerifyBackup(ctx, backupDir)
	if err == nil || repC.Status != "failed" {
		t.Errorf("expected verify failure on unsupported format_version, got nil")
	}

	// Case D: Unsupported future schema_version
	_ = json.Unmarshal(origManifestBytes, &m)
	m.SchemaVersion = 999
	mBytes, _ = json.Marshal(m)
	_ = os.WriteFile(manifestPath, mBytes, 0600)

	repD, err := svc.VerifyBackup(ctx, backupDir)
	if err == nil || repD.Status != "failed" {
		t.Errorf("expected verify failure on unsupported schema_version, got nil")
	}
}

func TestRestoreActiveTargetRejected(t *testing.T) {
	ctx := context.Background()
	srcDir := setupTestDB(t)
	backupDir := filepath.Join(t.TempDir(), "bk")
	targetDir := filepath.Join(t.TempDir(), "active_target")

	svc := backup.NewService()
	_, err := svc.CreateBackup(ctx, srcDir, backupDir, false)
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}

	// Lock the target directory to simulate an active running service
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatalf("mkdir targetDir: %v", err)
	}
	lock, err := platform.AcquireProcessLock(targetDir)
	if err != nil {
		t.Fatalf("acquire lock failed: %v", err)
	}
	defer lock.Release()

	// Attempt restore into active directory
	_, err = svc.RestoreBackup(ctx, backupDir, targetDir)
	if err != backup.ErrActiveTargetDirectory {
		t.Errorf("expected ErrActiveTargetDirectory, got %v", err)
	}
}

func TestRestoreMaintenanceStatePreserved(t *testing.T) {
	ctx := context.Background()
	srcDir := setupTestDB(t)
	backupDir := filepath.Join(t.TempDir(), "maint_bk")
	targetDir := filepath.Join(t.TempDir(), "maint_target")

	// Set source DB into rebuilding mode
	db, err := platform.OpenDB(srcDir)
	if err != nil {
		t.Fatalf("OpenDB failed: %v", err)
	}
	_, err = db.Exec("UPDATE projection_control SET mode = 'rebuilding', requires_rebuild = 1 WHERE id = 1")
	db.Close()
	if err != nil {
		t.Fatalf("set rebuilding mode failed: %v", err)
	}

	svc := backup.NewService()
	_, err = svc.CreateBackup(ctx, srcDir, backupDir, false)
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}

	_, err = svc.RestoreBackup(ctx, backupDir, targetDir)
	if err != nil {
		t.Fatalf("RestoreBackup failed: %v", err)
	}

	// Verify restored DB still has mode = 'rebuilding'
	rDB, err := platform.OpenDB(targetDir)
	if err != nil {
		t.Fatalf("Open restored DB failed: %v", err)
	}
	defer rDB.Close()

	var mode string
	var reqReb int
	err = rDB.QueryRow("SELECT mode, requires_rebuild FROM projection_control WHERE id = 1").Scan(&mode, &reqReb)
	if err != nil {
		t.Fatalf("query projection_control failed: %v", err)
	}
	if mode != "rebuilding" {
		t.Errorf("expected mode rebuilding, got %s", mode)
	}
	if reqReb != 1 {
		t.Errorf("expected requires_rebuild 1, got %d", reqReb)
	}
}

func TestBackupCheckpointFailureReturnsError(t *testing.T) {
	ctx := context.Background()
	srcDir := setupTestDB(t)
	backupDir := filepath.Join(t.TempDir(), "checkpoint_fail_backup")

	// Create a corrupted langgraph-checkpoints.db in srcDir
	corruptChkPath := filepath.Join(srcDir, "langgraph-checkpoints.db")
	if err := os.WriteFile(corruptChkPath, []byte("this is not a valid sqlite database"), 0600); err != nil {
		t.Fatalf("write corrupt checkpoint: %v", err)
	}

	svc := backup.NewService()
	_, err := svc.CreateBackup(ctx, srcDir, backupDir, true)
	if err == nil {
		t.Fatalf("expected CreateBackup to fail when checkpoint database is corrupt, got nil")
	}
}

func TestCopyFileErrorHandling(t *testing.T) {
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "source.txt")
	dstFile := filepath.Join(tmpDir, "dest.txt")

	if err := os.WriteFile(srcFile, []byte("hello backup"), 0600); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	// 1. Non-existent source
	svc := backup.NewService()
	_ = svc
	if err := backup.CopyFileForTest(filepath.Join(tmpDir, "non_existent.txt"), dstFile); err == nil {
		t.Errorf("expected error copying from non-existent source, got nil")
	}

	// 2. Unwritable destination directory
	badDst := filepath.Join(tmpDir, "no_such_dir", "dest.txt")
	if err := backup.CopyFileForTest(srcFile, badDst); err == nil {
		t.Errorf("expected error copying to unwritable destination, got nil")
	}

	// 3. Successful copy
	if err := backup.CopyFileForTest(srcFile, dstFile); err != nil {
		t.Fatalf("copyFile failed: %v", err)
	}
	content, err := os.ReadFile(dstFile)
	if err != nil || string(content) != "hello backup" {
		t.Errorf("copied content mismatch: got %q, err=%v", string(content), err)
	}
}


