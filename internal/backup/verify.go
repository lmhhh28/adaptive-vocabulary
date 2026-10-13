package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"adaptive-vocabulary/internal/core"
	_ "modernc.org/sqlite"
)

// VerifyBackup validates the integrity, structure, checksums, and SQLite consistency of a backup directory.
func (s *Service) VerifyBackup(ctx context.Context, inputDir string) (*VerifyReport, error) {
	opID := core.NewUUID()
	report := &VerifyReport{
		OperationID: opID,
		Status:      "ok",
		Checks:      make(map[string]string),
	}

	manifestPath := filepath.Join(inputDir, "manifest.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		report.Status = "failed"
		report.Errors = append(report.Errors, fmt.Sprintf("manifest.json missing or unreadable: %v", err))
		return report, fmt.Errorf("%w: %v", ErrManifestNotFound, err)
	}
	report.InputManifest = manifestPath

	var m Manifest
	if err := json.Unmarshal(manifestData, &m); err != nil {
		report.Status = "failed"
		report.Errors = append(report.Errors, fmt.Sprintf("invalid manifest.json syntax: %v", err))
		return report, fmt.Errorf("parse manifest.json: %w", err)
	}
	report.EventWatermark = m.SnapshotWatermark

	// 1. Format and Schema version checks
	if m.FormatVersion != "1.0.0" {
		report.Errors = append(report.Errors, fmt.Sprintf("unsupported format_version %q, expected 1.0.0", m.FormatVersion))
		report.Checks["format_version"] = "failed"
	} else {
		report.Checks["format_version"] = "ok"
	}

	if m.SchemaVersion > 100 {
		report.Errors = append(report.Errors, fmt.Sprintf("unsupported future schema_version %d (current max 100)", m.SchemaVersion))
		report.Checks["schema_version"] = "failed"
	} else if m.SchemaVersion < 0 {
		report.Errors = append(report.Errors, fmt.Sprintf("invalid schema_version %d", m.SchemaVersion))
		report.Checks["schema_version"] = "failed"
	} else {
		report.Checks["schema_version"] = "ok"
	}

	// 2. Files path security and checksum validation
	hasAppDB := false
	for _, f := range m.Files {
		cleanPath := filepath.Clean(f.RelativePath)
		// Reject path traversal, absolute paths, and parent references
		if cleanPath == "" || cleanPath == "." || filepath.IsAbs(f.RelativePath) ||
			strings.HasPrefix(f.RelativePath, "/") || strings.HasPrefix(f.RelativePath, "\\") ||
			strings.Contains(f.RelativePath, "..") || strings.HasPrefix(cleanPath, "..") {
			report.Errors = append(report.Errors, fmt.Sprintf("path traversal or invalid relative path rejected: %q", f.RelativePath))
			report.Checks["path_security_"+cleanPath] = "failed"
			continue
		}

		targetPath := filepath.Join(inputDir, cleanPath)
		// Ensure resolved path is strictly within inputDir
		rel, err := filepath.Rel(inputDir, targetPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			report.Errors = append(report.Errors, fmt.Sprintf("path escapes backup directory: %q", f.RelativePath))
			report.Checks["path_security_"+cleanPath] = "failed"
			continue
		}

		if cleanPath == "app.db" {
			hasAppDB = true
		}

		actualBytes, actualHash, err := computeFileHash(targetPath)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("file %s missing or unreadable: %v", cleanPath, err))
			report.Checks["file_"+cleanPath] = "missing"
			continue
		}

		if actualBytes != f.Bytes {
			report.Errors = append(report.Errors, fmt.Sprintf("file %s size mismatch: got %d, expected %d", cleanPath, actualBytes, f.Bytes))
			report.Checks["file_"+cleanPath] = "size_mismatch"
			continue
		}

		if !strings.EqualFold(actualHash, f.SHA256) {
			report.Errors = append(report.Errors, fmt.Sprintf("file %s checksum mismatch: got %s, expected %s", cleanPath, actualHash, f.SHA256))
			report.Checks["file_"+cleanPath] = "checksum_mismatch"
			continue
		}

		report.Checks["file_"+cleanPath] = "ok"
	}

	if !hasAppDB {
		report.Errors = append(report.Errors, "manifest does not contain app.db database")
		report.Checks["app_db_present"] = "failed"
	}

	// 3. SQLite Database Consistency Checks
	appDBPath := filepath.Join(inputDir, "app.db")
	if hasAppDB {
		if _, err := os.Stat(appDBPath); err == nil {
			db, err := sql.Open("sqlite", fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", appDBPath))
			if err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("failed to open app.db: %v", err))
				report.Checks["db_open"] = "failed"
			} else {
				defer db.Close()

				// PRAGMA integrity_check
				var integrity string
				if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
					report.Errors = append(report.Errors, fmt.Sprintf("integrity_check failed: %s (%v)", integrity, err))
					report.Checks["integrity_check"] = "failed"
				} else {
					report.Checks["integrity_check"] = "ok"
				}

				// PRAGMA foreign_key_check
				fkRows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
				if err != nil {
					report.Errors = append(report.Errors, fmt.Sprintf("foreign_key_check failed: %v", err))
					report.Checks["foreign_key_check"] = "failed"
				} else {
					fkErrors := 0
					for fkRows.Next() {
						fkErrors++
					}
					fkRows.Close()
					if fkErrors > 0 {
						report.Errors = append(report.Errors, fmt.Sprintf("foreign_key_check found %d violations", fkErrors))
						report.Checks["foreign_key_check"] = "failed"
					} else {
						report.Checks["foreign_key_check"] = "ok"
					}
				}

				// Watermark verification
				var dbWatermark int64
				err = db.QueryRowContext(ctx, "SELECT coalesce(max(seq), 0) FROM global_sequence").Scan(&dbWatermark)
				if err != nil || dbWatermark != m.SnapshotWatermark {
					report.Errors = append(report.Errors, fmt.Sprintf("watermark mismatch: snapshot db has %d, manifest declared %d", dbWatermark, m.SnapshotWatermark))
					report.Checks["watermark_match"] = "failed"
				} else {
					report.Checks["watermark_match"] = "ok"
				}

				// Entity reference closure checks
				var orphanInstances int
				queryOrphanInst := "SELECT COUNT(*) FROM question_instance qi LEFT JOIN question_version qv ON qv.id = qi.question_version_id WHERE qv.id IS NULL"
				_ = db.QueryRowContext(ctx, queryOrphanInst).Scan(&orphanInstances)
				if orphanInstances > 0 {
					report.Errors = append(report.Errors, fmt.Sprintf("found %d orphaned question_instances", orphanInstances))
					report.Checks["reference_instances"] = "failed"
				} else {
					report.Checks["reference_instances"] = "ok"
				}

				var orphanAttempts int
				queryOrphanAtt := "SELECT COUNT(*) FROM attempt_event ae LEFT JOIN question_instance qi ON qi.id = ae.instance_id WHERE qi.id IS NULL"
				_ = db.QueryRowContext(ctx, queryOrphanAtt).Scan(&orphanAttempts)
				if orphanAttempts > 0 {
					report.Errors = append(report.Errors, fmt.Sprintf("found %d orphaned attempt_events", orphanAttempts))
					report.Checks["reference_attempts"] = "failed"
				} else {
					report.Checks["reference_attempts"] = "ok"
				}
			}
		}
	}

	// 4. Optional Checkpoint DB Check
	chkPath := filepath.Join(inputDir, "langgraph-checkpoints.db")
	if _, err := os.Stat(chkPath); err == nil {
		chkDB, err := sql.Open("sqlite", fmt.Sprintf("%s?_pragma=busy_timeout(5000)", chkPath))
		if err == nil {
			var chkInteg string
			if err := chkDB.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&chkInteg); err == nil && chkInteg == "ok" {
				report.Checks["checkpoint_integrity"] = "ok"
			} else {
				report.Errors = append(report.Errors, fmt.Sprintf("checkpoint integrity check failed: %s (%v)", chkInteg, err))
				report.Checks["checkpoint_integrity"] = "failed"
			}
			chkDB.Close()
		}
	}

	if len(report.Errors) > 0 {
		report.Status = "failed"
		return report, fmt.Errorf("%w: %s", ErrVerificationFailed, strings.Join(report.Errors, "; "))
	}

	report.Status = "ok"
	return report, nil
}
