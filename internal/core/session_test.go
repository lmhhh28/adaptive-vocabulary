package core_test

import (
	"context"
	"testing"

	"adaptive-vocabulary/internal/core"
)

func TestSession_SingleActiveConstraint(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 1. Insert first open session
	err := core.RunInTx(ctx, db.DB, func(tc *core.TxnContext) error {
		query := `
			INSERT INTO session (id, kind, status, created_at, updated_at)
			VALUES ('sess-1', 'practice', 'open', 1000, 1000)
		`
		_, err := tc.Exec(query)
		return err
	})
	if err != nil {
		t.Fatalf("insert sess-1 failed: %v", err)
	}

	// Active session should be sess-1
	active, err := core.GetActiveSession(db.DB)
	if err != nil {
		t.Fatalf("GetActiveSession failed: %v", err)
	}
	if active == nil || active.ID != "sess-1" {
		t.Fatalf("expected active session sess-1, got %+v", active)
	}

	// 2. Attempt to insert second open session -> MUST FAIL due to uq_session_active
	err = core.RunInTx(ctx, db.DB, func(tc *core.TxnContext) error {
		query := `
			INSERT INTO session (id, kind, status, created_at, updated_at)
			VALUES ('sess-2', 'exam', 'open', 2000, 2000)
		`
		_, err := tc.Exec(query)
		return err
	})
	if err == nil {
		t.Fatalf("expected error inserting second active session, got nil")
	}

	// 3. End sess-1
	err = core.RunInTx(ctx, db.DB, func(tc *core.TxnContext) error {
		query := `UPDATE session SET status = 'ended', updated_at = 3000 WHERE id = 'sess-1'`
		_, err := tc.Exec(query)
		return err
	})
	if err != nil {
		t.Fatalf("end sess-1 failed: %v", err)
	}

	// Now no active session
	active, err = core.GetActiveSession(db.DB)
	if err != nil {
		t.Fatalf("GetActiveSession after end failed: %v", err)
	}
	if active != nil {
		t.Fatalf("expected no active session, got %+v", active)
	}

	// 4. Now inserting sess-2 as open should succeed!
	err = core.RunInTx(ctx, db.DB, func(tc *core.TxnContext) error {
		query := `
			INSERT INTO session (id, kind, status, created_at, updated_at)
			VALUES ('sess-2', 'exam', 'open', 4000, 4000)
		`
		_, err := tc.Exec(query)
		return err
	})
	if err != nil {
		t.Fatalf("insert sess-2 failed: %v", err)
	}

	active, err = core.GetActiveSession(db.DB)
	if err != nil {
		t.Fatalf("GetActiveSession after sess-2 failed: %v", err)
	}
	if active == nil || active.ID != "sess-2" {
		t.Fatalf("expected active session sess-2, got %+v", active)
	}
}
