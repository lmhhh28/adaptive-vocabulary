package backup

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"adaptive-vocabulary/internal/core"
	"adaptive-vocabulary/internal/platform"
	_ "modernc.org/sqlite"
)

// RestoreBackup verifies the backup in inputDir, ensures targetDir is non-active, copies the data,
// invalidates in-flight leases with fence increments, retains unknown budget reservations, and verifies the restored DB.
func (s *Service) RestoreBackup(ctx context.Context, inputDir, targetDir string) (*RestoreReport, error) {
	opID := core.NewUUID()

	// 1. Verify the backup first
	verifyReport, err := s.VerifyBackup(ctx, inputDir)
	if err != nil || verifyReport.Status != "ok" {
		errMsg := "backup verification failed"
		if err != nil {
			errMsg = err.Error()
		}
		return &RestoreReport{
			OperationID:     opID,
			Status:          "failed",
			TargetDirectory: targetDir,
			Errors:          []string{errMsg},
		}, fmt.Errorf("%w: %s", ErrVerificationFailed, errMsg)
	}

	absTargetDir, err := filepath.Abs(targetDir)
	if err != nil {
		return nil, fmt.Errorf("resolve target directory: %w", err)
	}

	// Ensure target directory exists with 0700 permissions
	if err := os.MkdirAll(absTargetDir, 0700); err != nil {
		return nil, fmt.Errorf("create target directory: %w", err)
	}
	if err := os.Chmod(absTargetDir, 0700); err != nil {
		return nil, fmt.Errorf("chmod target directory: %w", err)
	}

	// 2. Check if target directory has active running process
	lock, err := platform.AcquireProcessLock(absTargetDir)
	if err != nil {
		return &RestoreReport{
			OperationID:     opID,
			Status:          "failed",
			TargetDirectory: absTargetDir,
			Errors:          []string{fmt.Sprintf("target directory is active: %v", err)},
		}, ErrActiveTargetDirectory
	}
	defer func() { _ = lock.Release() }()
	entries, err := os.ReadDir(absTargetDir)
	if err != nil {
		return nil, fmt.Errorf("inspect restore destination: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() != "process.lock" {
			return nil, fmt.Errorf("restore target must be an empty new directory: %s", absTargetDir)
		}
	}

	// 3. Copy snapshot files into targetDir
	srcAppDB := filepath.Join(inputDir, "app.db")
	dstAppDB := filepath.Join(absTargetDir, "app.db")
	_ = os.Remove(dstAppDB)
	_ = os.Remove(dstAppDB + "-wal")
	_ = os.Remove(dstAppDB + "-shm")

	if err := copyFile(srcAppDB, dstAppDB); err != nil {
		return nil, fmt.Errorf("copy app.db: %w", err)
	}
	_ = os.Chmod(dstAppDB, 0600)

	srcManifest := filepath.Join(inputDir, "manifest.json")
	dstManifest := filepath.Join(absTargetDir, "manifest.json")
	_ = copyFile(srcManifest, dstManifest)
	_ = os.Chmod(dstManifest, 0600)

	checkpointsPresent := false
	srcChk := filepath.Join(inputDir, "langgraph-checkpoints.db")
	if _, err := os.Stat(srcChk); err == nil {
		dstChk := filepath.Join(absTargetDir, "langgraph-checkpoints.db")
		_ = os.Remove(dstChk)
		_ = os.Remove(dstChk + "-wal")
		_ = os.Remove(dstChk + "-shm")
		if err := copyFile(srcChk, dstChk); err == nil {
			checkpointsPresent = true
			_ = os.Chmod(dstChk, 0600)
		}
	}

	tokenPath := filepath.Join(absTargetDir, "worker.token")
	if _, err := os.Stat(tokenPath); os.IsNotExist(err) {
		tokenBytes := make([]byte, 32)
		_, _ = rand.Read(tokenBytes)
		_ = os.WriteFile(tokenPath, []byte(fmt.Sprintf("%x\n", tokenBytes)), 0600)
	}

	// 4. Open restored target app.db and reconcile
	dbDSN := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", dstAppDB)
	db, err := sql.Open("sqlite", dbDSN)
	if err != nil {
		return nil, fmt.Errorf("open restored app.db: %w", err)
	}
	defer db.Close()

	nowMicro := time.Now().UTC().UnixNano() / 1000
	var invalidatedLeases int
	var unknownCalls int

	err = core.RunInTx(ctx, db, func(tc *core.TxnContext) error {
		// A. Invalidate uncompleted generation leases and increment fencing_token
		type claimedItem struct {
			id      string
			fence   int64
			attempt int
		}
		var claimed []claimedItem
		rows, err := tc.Query("SELECT id, fencing_token, attempt_number FROM generation_item WHERE status = 'claimed'")
		if err != nil {
			return fmt.Errorf("query claimed generation items: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var c claimedItem
			if err := rows.Scan(&c.id, &c.fence, &c.attempt); err != nil {
				return fmt.Errorf("scan claimed generation item: %w", err)
			}
			claimed = append(claimed, c)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate claimed generation items: %w", err)
		}

		for _, c := range claimed {
			_, err := tc.Exec(`
				UPDATE generation_item
				SET status = 'pending', lease_owner = NULL, lease_until = 0,
				    fencing_token = ?, attempt_number = ?, revision = revision + 1, updated_at = ?
				WHERE id = ?
			`, c.fence+1, c.attempt+1, nowMicro, c.id)
			if err != nil {
				return fmt.Errorf("invalidate claimed item %s: %w", c.id, err)
			}
			invalidatedLeases++
		}

		// B. Mark reserved generation calls as unknown
		res, err := tc.Exec(`
			UPDATE generation_call
			SET status = 'unknown', updated_at = ?
			WHERE status = 'reserved'
		`, nowMicro)
		if err != nil {
			return fmt.Errorf("mark reserved calls unknown: %w", err)
		}
		affected, _ := res.RowsAffected()
		unknownCalls = int(affected)

		// C. Maintenance mode preservation:
		var pcMode string
		var reqRebuild int
		err = tc.QueryRow("SELECT mode, requires_rebuild FROM projection_control WHERE id = 1").Scan(&pcMode, &reqRebuild)
		if err == nil && pcMode == "rebuilding" {
			_, _ = tc.Exec("UPDATE projection_control SET mode = 'rebuilding', requires_rebuild = 1, updated_at = ? WHERE id = 1", nowMicro)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reconcile restored database state: %w", err)
	}

	// 5. Checkpoint reconciliation
	checkpointsReconciled := false
	var threadIDs []string
	if checkpointsPresent {
		dstChk := filepath.Join(absTargetDir, "langgraph-checkpoints.db")
		chkDB, err := sql.Open("sqlite", fmt.Sprintf("%s?_pragma=busy_timeout(5000)", dstChk))
		if err == nil {
			tRows, err := chkDB.QueryContext(ctx, "SELECT DISTINCT thread_id FROM checkpoints")
			if err == nil {
				for tRows.Next() {
					var tid string
					if err := tRows.Scan(&tid); err == nil {
						threadIDs = append(threadIDs, tid)
					}
				}
				tRows.Close()
			}
			checkpointsReconciled = true
			chkDB.Close()
		}
	}

	// 6. Post-restore integrity and foreign key checks
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return nil, fmt.Errorf("%w: %s (%v)", ErrIntegrityCheckFailed, integrity, err)
	}

	fkRows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err == nil {
		fkErrors := 0
		for fkRows.Next() {
			fkErrors++
		}
		fkRows.Close()
		if fkErrors > 0 {
			return nil, fmt.Errorf("foreign_key_check failed on restored DB: %d violations", fkErrors)
		}
	}

	report := &RestoreReport{
		OperationID:     opID,
		Status:          "ok",
		TargetDirectory: absTargetDir,
		EventWatermark:  verifyReport.EventWatermark,
		Reconciliation: ReconciliationReport{
			InvalidatedLeasesCount: invalidatedLeases,
			UnknownCallsCount:      unknownCalls,
			CheckpointsReconciled:  checkpointsReconciled,
			Details: map[string]any{
				"checkpoint_threads_found": len(threadIDs),
			},
		},
		Checks: verifyReport.Checks,
	}

	return report, nil
}
