package revision_test

import (
	"context"
	"testing"
	"time"

	"adaptive-vocabulary/internal/content"
	"adaptive-vocabulary/internal/core"
	"adaptive-vocabulary/internal/maintenance"
	"adaptive-vocabulary/internal/platform"
	"adaptive-vocabulary/internal/revision"
)

func setupTestDB(t *testing.T) (*platform.DB, *revision.Service, func()) {
	t.Helper()
	dir := t.TempDir()
	db, err := platform.OpenDB(dir)
	if err != nil {
		t.Fatalf("OpenDB failed: %v", err)
	}

	_, err = platform.RunMigrations(db.DB)
	if err != nil {
		_ = db.Close()
		t.Fatalf("RunMigrations failed: %v", err)
	}

	if err := content.ApplySeed(context.Background(), db.DB, "core"); err != nil {
		_ = db.Close()
		t.Fatalf("ApplySeed failed: %v", err)
	}

	maintSvc := maintenance.NewService(db.DB)
	revSvc := revision.NewService(db.DB, maintSvc)
	cleanup := func() {
		_ = db.Close()
	}
	return db, revSvc, cleanup
}

func TestApplyEvidenceDisposition_ActiveSessionConflict(t *testing.T) {
	db, svc, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Insert an open session
	sessID := core.NewUUID()
	now := time.Now().UTC().UnixNano() / 1000
	_, err := db.DB.Exec(
		"INSERT INTO session (id, kind, status, cursor, revision, created_at, updated_at) VALUES (?, 'practice', 'open', 0, 0, ?, ?)",
		sessID, now, now,
	)
	if err != nil {
		t.Fatalf("insert open session failed: %v", err)
	}

	// Fetch a question_version
	var qvID string
	err = db.DB.QueryRow("SELECT id FROM question_version LIMIT 1").Scan(&qvID)
	if err != nil {
		t.Fatalf("query question_version failed: %v", err)
	}

	// Attempt disposition: must fail with ErrSessionActive
	_, err = svc.ApplyEvidenceDisposition(ctx, qvID, "disputed", "test error", nil)
	if err == nil {
		t.Fatalf("expected error due to active session, got nil")
	}

	// Close the session
	_, err = db.DB.Exec("UPDATE session SET status = 'ended' WHERE id = ?", sessID)
	if err != nil {
		t.Fatalf("close session failed: %v", err)
	}

	// Attempt disposition again: should succeed
	resp, err := svc.ApplyEvidenceDisposition(ctx, qvID, "disputed", "test error", nil)
	if err != nil {
		t.Fatalf("ApplyEvidenceDisposition failed after closing session: %v", err)
	}
	if resp.Status != "succeeded" {
		t.Errorf("expected status succeeded, got %s", resp.Status)
	}

	// Check question_version status is now disputed
	var status string
	_ = db.DB.QueryRow("SELECT status FROM question_version WHERE id = ?", qvID).Scan(&status)
	if status != "disputed" {
		t.Errorf("expected question_version status disputed, got %s", status)
	}
}

func TestApplyEvidenceDisposition_Invalidate_And_Restore(t *testing.T) {
	db, svc, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	var qvID string
	err := db.DB.QueryRow("SELECT id FROM question_version LIMIT 1").Scan(&qvID)
	if err != nil {
		t.Fatalf("query question_version failed: %v", err)
	}

	// Invalidate
	resp, err := svc.ApplyEvidenceDisposition(ctx, qvID, "invalidate", "wrong answer key", nil)
	if err != nil {
		t.Fatalf("invalidate failed: %v", err)
	}
	if resp.Status != "succeeded" {
		t.Errorf("expected succeeded, got %s", resp.Status)
	}

	var status string
	_ = db.DB.QueryRow("SELECT status FROM question_version WHERE id = ?", qvID).Scan(&status)
	if status != "invalidated" {
		t.Errorf("expected invalidated, got %s", status)
	}

	// Restore validity
	resp2, err := svc.ApplyEvidenceDisposition(ctx, qvID, "restore_validity", "restored after verification", nil)
	if err != nil {
		t.Fatalf("restore_validity failed: %v", err)
	}
	if resp2.Status != "succeeded" {
		t.Errorf("expected succeeded, got %s", resp2.Status)
	}

	_ = db.DB.QueryRow("SELECT status FROM question_version WHERE id = ?", qvID).Scan(&status)
	if status != "published" {
		t.Errorf("expected published, got %s", status)
	}
}

func TestRetireQuestionVersion(t *testing.T) {
	db, svc, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	var qvID string
	err := db.DB.QueryRow("SELECT id FROM question_version LIMIT 1").Scan(&qvID)
	if err != nil {
		t.Fatalf("query question_version failed: %v", err)
	}

	resp, err := svc.RetireQuestionVersion(ctx, qvID, "outdated item", nil)
	if err != nil {
		t.Fatalf("RetireQuestionVersion failed: %v", err)
	}
	if resp.Status != "retired" {
		t.Errorf("expected status retired, got %s", resp.Status)
	}
}
