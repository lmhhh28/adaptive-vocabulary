package core_test

import (
	"context"
	"errors"
	"testing"

	"adaptive-vocabulary/internal/core"
	"adaptive-vocabulary/internal/platform"
)

func setupTestDB(t *testing.T) *platform.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := platform.OpenDB(dir)
	if err != nil {
		t.Fatalf("OpenDB failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = platform.RunMigrations(db.DB)
	if err != nil {
		t.Fatalf("RunMigrations failed: %v", err)
	}
	return db
}

func TestRunInTx_Commit(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	err := core.RunInTx(ctx, db.DB, func(tc *core.TxnContext) error {
		_, err := tc.Exec("INSERT INTO global_sequence (event_name, created_at) VALUES (?, ?)", "test_event", 1000)
		return err
	})
	if err != nil {
		t.Fatalf("RunInTx failed: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM global_sequence WHERE event_name = 'test_event'").Scan(&count); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 record, got %d", count)
	}
}

func TestRunInTx_Rollback(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	testErr := errors.New("simulated error")
	err := core.RunInTx(ctx, db.DB, func(tc *core.TxnContext) error {
		_, err := tc.Exec("INSERT INTO global_sequence (event_name, created_at) VALUES (?, ?)", "rollback_event", 1000)
		if err != nil {
			return err
		}
		return testErr
	})
	if !errors.Is(err, testErr) {
		t.Fatalf("expected testErr, got %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM global_sequence WHERE event_name = 'rollback_event'").Scan(&count); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 records after rollback, got %d", count)
	}
}
