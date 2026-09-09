package content

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"adaptive-vocabulary/internal/core"
	_ "modernc.org/sqlite"
)

func TestNormalizationAndHashing(t *testing.T) {
	norm := NormalizeText("   Hello   WORLD  ")
	if norm != "hello world" {
		t.Fatalf("expected 'hello world', got %q", norm)
	}

	key1 := ComputeOptionCanonicalKey("zh", " 河流 ", "sense-1", "src-1", "1.0")
	key2 := ComputeOptionCanonicalKey("zh", "河流", "sense-1", "src-1", "1.0")
	if key1 != key2 {
		t.Fatalf("expected identical keys for whitespace difference, got %s vs %s", key1, key2)
	}

	hash1 := ComputeFormDedupHash("sense-1", "meaning_recognition", " Stem 1 ", []string{"k2", "k1"})
	hash2 := ComputeFormDedupHash("sense-1", "meaning_recognition", "stem 1", []string{"k1", "k2"})
	if hash1 != hash2 {
		t.Fatalf("expected identical hashes regardless of option order, got %s vs %s", hash1, hash2)
	}
}

func TestSeedCoreIdempotency(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "avocab-content-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "app.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := core.RunMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	ctx := context.Background()

	// First seed core
	if err := ApplySeed(ctx, db, "core"); err != nil {
		t.Fatalf("apply seed core: %v", err)
	}

	var lexemeCount, senseCount, questionCount, bankCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM lexeme").Scan(&lexemeCount)
	_ = db.QueryRow("SELECT COUNT(*) FROM sense").Scan(&senseCount)
	_ = db.QueryRow("SELECT COUNT(*) FROM question_version").Scan(&questionCount)
	_ = db.QueryRow("SELECT COUNT(*) FROM bank").Scan(&bankCount)

	if lexemeCount != 8 {
		t.Errorf("expected 8 lexemes, got %d", lexemeCount)
	}
	if senseCount != 9 {
		t.Errorf("expected 9 senses, got %d", senseCount)
	}
	if questionCount != 14 {
		t.Errorf("expected 14 questions, got %d", questionCount)
	}
	if bankCount != 2 {
		t.Errorf("expected 2 banks, got %d", bankCount)
	}

	// Repeat seed core - must be idempotent
	if err := ApplySeed(ctx, db, "core"); err != nil {
		t.Fatalf("repeat seed core should succeed, got %v", err)
	}

	var lexemeCount2, questionCount2 int
	_ = db.QueryRow("SELECT COUNT(*) FROM lexeme").Scan(&lexemeCount2)
	_ = db.QueryRow("SELECT COUNT(*) FROM question_version").Scan(&questionCount2)
	if lexemeCount2 != lexemeCount || questionCount2 != questionCount {
		t.Errorf("repeat seed modified count: %d -> %d, %d -> %d",
			lexemeCount, lexemeCount2, questionCount, questionCount2)
	}

	// Unknown seed must fail
	if err := ApplySeed(ctx, db, "unknown_seed"); err == nil {
		t.Error("expected unknown seed to fail")
	}
}
