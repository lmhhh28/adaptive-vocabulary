package bank

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"adaptive-vocabulary/internal/content"
	"adaptive-vocabulary/internal/core"
	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) (*sql.DB, func()) {
	tmpDir, err := os.MkdirTemp("", "avocab-bank-test-*")
	if err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(tmpDir, "app.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := core.RunMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.RemoveAll(tmpDir)
	}

	return db, cleanup
}

func TestBankCRUDAndLifecycle(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Seed database with core
	if err := content.ApplySeed(ctx, db, "core"); err != nil {
		t.Fatalf("apply seed core: %v", err)
	}

	svc := NewService(db)

	// List initial banks from seed (Bank 1: 开发样例, Bank 2: 跨库共享样例)
	banks, err := svc.ListBanks(ctx)
	if err != nil {
		t.Fatalf("list banks: %v", err)
	}
	if len(banks) != 2 {
		t.Fatalf("expected 2 seeded banks, got %d", len(banks))
	}

	// Bank 1 has 9 members, Bank 2 has 3 members
	bank1 := banks[0]
	bank2 := banks[1]
	if bank1.MemberCount != 9 {
		t.Errorf("expected bank1 to have 9 members, got %d", bank1.MemberCount)
	}
	if bank2.MemberCount != 3 {
		t.Errorf("expected bank2 to have 3 members, got %d", bank2.MemberCount)
	}

	// Create custom bank
	newBank, err := svc.CreateBank(ctx, "Test Custom Bank", "A test bank")
	if err != nil {
		t.Fatalf("create bank: %v", err)
	}
	if newBank.Name != "Test Custom Bank" {
		t.Errorf("expected name 'Test Custom Bank', got %q", newBank.Name)
	}

	// Remove member from bank 2
	detail2, err := svc.GetBank(ctx, bank2.ID)
	if err != nil {
		t.Fatalf("get bank2 detail: %v", err)
	}
	targetSenseID := detail2.Members[0].SenseID

	// Test revision conflict
	_, _, _, err = svc.RemoveMembers(ctx, bank2.ID, []string{targetSenseID}, 999)
	if err != ErrRevisionConflict {
		t.Fatalf("expected revision conflict, got %v", err)
	}

	// Remove member with correct expected_revision (0)
	removedCount, newRev, _, err := svc.RemoveMembers(ctx, bank2.ID, []string{targetSenseID}, 0)
	if err != nil {
		t.Fatalf("remove members: %v", err)
	}
	if removedCount != 1 {
		t.Errorf("expected 1 removed member, got %d", removedCount)
	}
	if newRev != 1 {
		t.Errorf("expected new revision 1, got %d", newRev)
	}

	// Verify global sense still exists in database and bank 1 is untouched
	var senseExists int
	_ = db.QueryRow("SELECT COUNT(*) FROM sense WHERE id = ?", targetSenseID).Scan(&senseExists)
	if senseExists != 1 {
		t.Errorf("global sense was deleted or missing!")
	}

	bank1Detail, _ := svc.GetBank(ctx, bank1.ID)
	if bank1Detail.MemberCount != 9 {
		t.Errorf("bank1 was unexpectedly modified, count=%d", bank1Detail.MemberCount)
	}

	// Archive bank 2 with revision 1
	archivedRev, _, err := svc.ArchiveBank(ctx, bank2.ID, 1)
	if err != nil {
		t.Fatalf("archive bank: %v", err)
	}
	if archivedRev != 2 {
		t.Errorf("expected archive revision 2, got %d", archivedRev)
	}

	archivedDetail, _ := svc.GetBank(ctx, bank2.ID)
	if !archivedDetail.Archived {
		t.Errorf("expected bank2 to be archived")
	}
	if len(archivedDetail.Members) != 2 {
		t.Errorf("archived bank should preserve its remaining members, got %d", len(archivedDetail.Members))
	}
}
