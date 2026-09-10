package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"adaptive-vocabulary/internal/core"
	_ "modernc.org/sqlite"
)

var (
	ErrActiveTargetDirectory = errors.New("target directory has an active running service or locked process")
	ErrManifestNotFound      = errors.New("manifest.json not found in backup directory")
	ErrIntegrityCheckFailed  = errors.New("database integrity check failed")
	ErrVerificationFailed    = errors.New("backup verification failed")
)

type Service struct{}

func NewService() *Service {
	return &Service{}
}

// CreateBackup creates a consistent snapshot of the data directory and writes manifest.json into outputDir.
func (s *Service) CreateBackup(ctx context.Context, dataDir, outputDir string, includeCheckpoints bool) (*Manifest, error) {
	appDBPath := filepath.Join(dataDir, "app.db")
	if _, err := os.Stat(appDBPath); err != nil {
		return nil, fmt.Errorf("source app.db not found at %s: %w", appDBPath, err)
	}

	absOutputDir, err := filepath.Abs(outputDir)
	if err != nil {
		return nil, fmt.Errorf("resolve output directory: %w", err)
	}
	if _, err := os.Lstat(absOutputDir); err == nil {
		return nil, fmt.Errorf("backup output must be a new directory: %s", absOutputDir)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect output directory: %w", err)
	}

	// Create a temporary directory in the same parent directory to allow atomic rename
	parentDir := filepath.Dir(absOutputDir)
	if err := os.MkdirAll(parentDir, 0700); err != nil {
		return nil, fmt.Errorf("create parent directory: %w", err)
	}

	tempDir := fmt.Sprintf("%s.tmp_%s", absOutputDir, core.NewUUID()[:8])
	if err := os.MkdirAll(tempDir, 0700); err != nil {
		return nil, fmt.Errorf("create temp backup dir: %w", err)
	}
	defer func() {
		// Clean up tempDir if it still exists (on error)
		if _, err := os.Stat(tempDir); err == nil {
			_ = os.RemoveAll(tempDir)
		}
	}()

	// 1. Consistent snapshot using VACUUM INTO
	tempAppDB := filepath.Join(tempDir, "app.db")
	_ = os.Remove(tempAppDB)

	srcDSN := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", appDBPath)
	srcDB, err := sql.Open("sqlite", srcDSN)
	if err != nil {
		return nil, fmt.Errorf("open source db: %w", err)
	}
	defer srcDB.Close()

	escapedTemp := strings.ReplaceAll(tempAppDB, "'", "''")
	if _, err := srcDB.ExecContext(ctx, fmt.Sprintf("VACUUM INTO '%s'", escapedTemp)); err != nil {
		return nil, fmt.Errorf("vacuum into snapshot: %w", err)
	}
	srcDB.Close() // Release source connection

	// 2. Read watermark and versions strictly from the snapshot itself
	snapDSN := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", tempAppDB)
	snapDB, err := sql.Open("sqlite", snapDSN)
	if err != nil {
		return nil, fmt.Errorf("open snapshot db: %w", err)
	}
	defer snapDB.Close()

	var watermark int64
	err = snapDB.QueryRowContext(ctx, "SELECT coalesce(max(seq), 0) FROM global_sequence").Scan(&watermark)
	if err != nil {
		return nil, fmt.Errorf("read snapshot watermark: %w", err)
	}

	var schemaVersion int
	err = snapDB.QueryRowContext(ctx, "SELECT coalesce(max(version), 0) FROM schema_migrations").Scan(&schemaVersion)
	if err != nil {
		return nil, fmt.Errorf("read snapshot schema_version: %w", err)
	}

	var projectionVersion string
	err = snapDB.QueryRowContext(ctx, "SELECT active_version FROM projection_control WHERE id = 1").Scan(&projectionVersion)
	if err != nil {
		projectionVersion = "v1"
	}

	var sqliteVer, sqliteSrc string
	_ = snapDB.QueryRowContext(ctx, "SELECT sqlite_version(), sqlite_source_id()").Scan(&sqliteVer, &sqliteSrc)

	var integrity string
	if err := snapDB.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return nil, fmt.Errorf("snapshot integrity_check failed: %s (%v)", integrity, err)
	}
	snapDB.Close()

	// 3. Compute hash and size for app.db
	dbBytes, dbHash, err := computeFileHash(tempAppDB)
	if err != nil {
		return nil, fmt.Errorf("hash snapshot db: %w", err)
	}

	files := []BackupFileInfo{
		{
			RelativePath: "app.db",
			Role:         "database",
			Bytes:        dbBytes,
			SHA256:       dbHash,
		},
	}

	// 4. Optionally snapshot checkpoint database
	if includeCheckpoints {
		chkPath := filepath.Join(dataDir, "langgraph-checkpoints.db")
		if _, err := os.Stat(chkPath); err == nil {
			tempChk := filepath.Join(tempDir, "langgraph-checkpoints.db")
			chkDB, err := sql.Open("sqlite", fmt.Sprintf("%s?_pragma=busy_timeout(5000)", chkPath))
			if err != nil {
				return nil, fmt.Errorf("open checkpoint db: %w", err)
			}
			escapedChk := strings.ReplaceAll(tempChk, "'", "''")
			if _, err := chkDB.ExecContext(ctx, fmt.Sprintf("VACUUM INTO '%s'", escapedChk)); err != nil {
				chkDB.Close()
				return nil, fmt.Errorf("snapshot checkpoint db: %w", err)
			}
			chkDB.Close()

			chkBytes, chkHash, err := computeFileHash(tempChk)
			if err != nil {
				return nil, fmt.Errorf("hash checkpoint db: %w", err)
			}
			files = append(files, BackupFileInfo{
				RelativePath: "langgraph-checkpoints.db",
				Role:         "checkpoint",
				Bytes:        chkBytes,
				SHA256:       chkHash,
			})
		}
	}

	// 5. Assemble manifest v1
	now := time.Now().UTC()
	manifest := &Manifest{
		FormatVersion:     "1.0.0",
		SchemaVersion:     schemaVersion,
		AppVersion:        "0.1.0",
		ContractVersion:   "1.1.0",
		PolicyVersion:     "policy-v2",
		ProjectionVersion: projectionVersion,
		SQLiteVersion:     sqliteVer,
		SQLiteSourceID:    sqliteSrc,
		SnapshotWatermark: watermark,
		CreatedAt:         now.UnixNano() / 1000,
		CreatedAtISO:      now.Format(time.RFC3339),
		Files:             files,
	}

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}

	manifestPath := filepath.Join(tempDir, "manifest.json")
	if err := os.WriteFile(manifestPath, append(manifestBytes, '\n'), 0600); err != nil {
		return nil, fmt.Errorf("write manifest.json: %w", err)
	}

	// 6. Atomically rename temp directory to target output directory
	if err := os.Chmod(tempDir, 0700); err != nil {
		return nil, fmt.Errorf("chmod temp dir: %w", err)
	}

	// Never delete an existing destination, including the source or a prior backup.
	if _, err := os.Lstat(absOutputDir); err == nil {
		return nil, fmt.Errorf("backup output appeared during snapshot: %s", absOutputDir)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect output directory before rename: %w", err)
	}

	if err := os.Rename(tempDir, absOutputDir); err != nil {
		return nil, fmt.Errorf("rename backup dir to %s: %w", absOutputDir, err)
	}

	return manifest, nil
}

func computeFileHash(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if cErr := out.Close(); err == nil && cErr != nil {
			err = cErr
		}
	}()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	return nil
}

// CopyFileForTest exports copyFile for package testing.
func CopyFileForTest(src, dst string) error {
	return copyFile(src, dst)
}

