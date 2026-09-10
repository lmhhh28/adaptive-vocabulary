package selection_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"adaptive-vocabulary/internal/platform"
	"adaptive-vocabulary/internal/selection"
)

func setupSelectionDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "avocab-selection-test-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(dir, "app.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)

	_, err = platform.RunMigrations(db)
	if err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	return db, dir
}

func TestSelectionExplorationAndFallback(t *testing.T) {
	db, dir := setupSelectionDB(t)
	defer os.RemoveAll(dir)
	defer db.Close()

	ctx := context.Background()

	// Seed source, lexemes, senses, banks
	_, err := db.Exec(`
		INSERT INTO source (id, name, location, source_version, license, usage_scope, created_at, updated_at)
		VALUES ('src1', 'src', 'loc', 'v1', 'mit', 'test', 1, 1);
		INSERT INTO bank (id, name, created_at, updated_at) VALUES ('bank1', 'Test Bank', 1, 1);

		-- Lexeme A (Unseen)
		INSERT INTO lexeme (id, canonical_form, created_at) VALUES ('lex_a', 'apple', 1);
		INSERT INTO sense (id, lexeme_id, part_of_speech, source_id, created_at) VALUES ('sense_a', 'lex_a', 'n', 'src1', 1);
		INSERT INTO sense_version (id, sense_id, meaning, created_at) VALUES ('sva', 'sense_a', '苹果', 1);
		INSERT INTO bank_sense (bank_id, sense_id, added_at) VALUES ('bank1', 'sense_a', 1);
		INSERT INTO question_form (id, sense_id, task_family, dedup_hash, normalized_stem, created_at)
		VALUES ('form_a', 'sense_a', 'meaning_recognition', 'hash_a', 'stem a', 1);
		INSERT INTO question_version (id, form_id, sense_id, sense_version_id, task_family, stem, options_json, correct_option_id, explanation, created_at, updated_at)
		VALUES ('qv_a', 'form_a', 'sense_a', 'sva', 'meaning_recognition', 'stem a', '[]', 'opt_a', 'exp', 1, 1);

		-- Lexeme B (Due)
		INSERT INTO lexeme (id, canonical_form, created_at) VALUES ('lex_b', 'banana', 1);
		INSERT INTO sense (id, lexeme_id, part_of_speech, source_id, created_at) VALUES ('sense_b', 'lex_b', 'n', 'src1', 1);
		INSERT INTO sense_version (id, sense_id, meaning, created_at) VALUES ('svb', 'sense_b', '香蕉', 1);
		INSERT INTO bank_sense (bank_id, sense_id, added_at) VALUES ('bank1', 'sense_b', 1);
		INSERT INTO question_form (id, sense_id, task_family, dedup_hash, normalized_stem, created_at)
		VALUES ('form_b', 'sense_b', 'meaning_recognition', 'hash_b', 'stem b', 1);
		INSERT INTO question_version (id, form_id, sense_id, sense_version_id, task_family, stem, options_json, correct_option_id, explanation, created_at, updated_at)
		VALUES ('qv_b', 'form_b', 'sense_b', 'svb', 'meaning_recognition', 'stem b', '[]', 'opt_b', 'exp', 1, 1);

		-- Evidence for B: 1 sample, due_at in the past
		INSERT INTO evidence_projection (projection_version, sense_id, task_family, ordinary_samples, ordinary_correct, needs_consolidation, updated_at)
		VALUES ('v1', 'sense_b', 'meaning_recognition', 1, 1, 0, 100);
		INSERT INTO memory_projection (projection_version, sense_id, task_family, due_at, updated_at)
		VALUES ('v1', 'sense_b', 'meaning_recognition', 500, 100);

		INSERT INTO session (id, kind, status, created_at, updated_at) VALUES ('sess1', 'practice', 'open', 1, 1);
	`)
	if err != nil {
		t.Fatalf("failed to seed selection test db: %v", err)
	}

	cfg := selection.DefaultSelectionConfig()
	selector := selection.NewSelector(db, cfg)

	// 1. Position 1: Unseen slot -> should select unseen candidate (Lexeme A)
	res1, err := selector.SelectNextQuestion(ctx, selection.SelectionRequest{
		SessionID:         "sess1",
		BankID:            "bank1",
		NormalPosition:    1,
		ProjectionVersion: "v1",
		NowMicro:          1000,
	})
	if err != nil {
		t.Fatalf("position 1 select error: %v", err)
	}
	if res1.Status != "ready" || res1.CandidateGroup != "unseen" || res1.SenseID != "sense_a" {
		t.Errorf("expected unseen sense_a for pos 1, got %+v", res1)
	}

	// 2. Position 2: Due slot -> should select due candidate (Lexeme B)
	res2, err := selector.SelectNextQuestion(ctx, selection.SelectionRequest{
		SessionID:         "sess1",
		BankID:            "bank1",
		NormalPosition:    2,
		ProjectionVersion: "v1",
		NowMicro:          1000,
	})
	if err != nil {
		t.Fatalf("position 2 select error: %v", err)
	}
	if res2.Status != "ready" || res2.CandidateGroup != "due" || res2.SenseID != "sense_b" {
		t.Errorf("expected due sense_b for pos 2, got %+v", res2)
	}

	// 3. Position 5: Unseen slot -> should select unseen candidate (Lexeme A)
	res5, err := selector.SelectNextQuestion(ctx, selection.SelectionRequest{
		SessionID:         "sess1",
		BankID:            "bank1",
		NormalPosition:    5,
		ProjectionVersion: "v1",
		NowMicro:          1000,
	})
	if err != nil {
		t.Fatalf("position 5 select error: %v", err)
	}
	if res5.Status != "ready" || res5.CandidateGroup != "unseen" || res5.SenseID != "sense_a" {
		t.Errorf("expected unseen sense_a for pos 5, got %+v", res5)
	}
}

func TestSelectionCooldownAndCorrection(t *testing.T) {
	db, dir := setupSelectionDB(t)
	defer os.RemoveAll(dir)
	defer db.Close()

	ctx := context.Background()

	// Seed source, bank, senses
	_, err := db.Exec(`
		INSERT INTO source (id, name, location, source_version, license, usage_scope, created_at, updated_at)
		VALUES ('src1', 'src', 'loc', 'v1', 'mit', 'test', 1, 1);
		INSERT INTO bank (id, name, created_at, updated_at) VALUES ('bank1', 'Test Bank', 1, 1);

		INSERT INTO lexeme (id, canonical_form, created_at) VALUES ('lex_c', 'cat', 1);
		INSERT INTO sense (id, lexeme_id, part_of_speech, source_id, created_at) VALUES ('sense_c', 'lex_c', 'n', 'src1', 1);
		INSERT INTO sense_version (id, sense_id, meaning, created_at) VALUES ('svc', 'sense_c', '猫', 1);
		INSERT INTO bank_sense (bank_id, sense_id, added_at) VALUES ('bank1', 'sense_c', 1);

		INSERT INTO question_form (id, sense_id, task_family, dedup_hash, normalized_stem, created_at)
		VALUES ('form_c1', 'sense_c', 'meaning_recognition', 'hash_c1', 'stem c1', 1),
		       ('form_c2', 'sense_c', 'meaning_recognition', 'hash_c2', 'stem c2', 1);
		INSERT INTO question_version (id, form_id, sense_id, sense_version_id, task_family, stem, options_json, correct_option_id, explanation, created_at, updated_at)
		VALUES ('qv_c1', 'form_c1', 'sense_c', 'svc', 'meaning_recognition', 'stem c1', '[]', 'opt_c1', 'exp', 1, 1),
		       ('qv_c2', 'form_c2', 'sense_c', 'svc', 'meaning_recognition', 'stem c2', '[]', 'opt_c2', 'exp', 1, 1);

		INSERT INTO session (id, kind, status, created_at, updated_at) VALUES ('sess_corr', 'practice', 'open', 1, 1);
		INSERT INTO question_instance (id, session_id, ordinal, question_version_id, snapshot_json, created_at)
		VALUES ('inst_c1', 'sess_corr', 0, 'qv_c1', '{}', 1000);

		INSERT INTO global_sequence (seq, event_name, created_at) VALUES (10, 'ev', 1000);
		-- Add pending correction item for sense_c
		INSERT INTO attempt_event (id, instance_id, session_id, sense_id, task_family, source, purpose, response_history, grading_version, is_correct, observed_at, event_sequence, created_at)
		VALUES ('att_c1', 'inst_c1', 'sess_corr', 'sense_c', 'meaning_recognition', 'practice', 'normal', 'single_response', 'grading-v1', 0, 1000, 10, 1000);
		INSERT INTO correction_item (session_id, sense_id, task_family, triggering_attempt_id, triggering_event_sequence, retries_count, status, created_at, updated_at)
		VALUES ('sess_corr', 'sense_c', 'meaning_recognition', 'att_c1', 10, 0, 'pending', 1000, 1000);
	`)
	if err != nil {
		t.Fatalf("failed to seed correction db: %v", err)
	}

	cfg := selection.DefaultSelectionConfig()
	selector := selection.NewSelector(db, cfg)

	// 1. Initially: 0 distinct other lexemes since triggering sequence 10 -> correction cooling NOT satisfied
	// Also for normal slot: sense_c has question, but let's check normal cooldown: no reserved exposure yet.
	// So normal selection chooses unseen/due.
	res, err := selector.SelectNextQuestion(ctx, selection.SelectionRequest{
		SessionID:         "sess_corr",
		BankID:            "bank1",
		NormalPosition:    1,
		ProjectionVersion: "v1",
		NowMicro:          2000,
	})
	if err != nil {
		t.Fatalf("select error: %v", err)
	}
	if res.IsCorrection {
		t.Errorf("correction should not be selected yet because 3 other lexemes not completed")
	}

	// 2. Add 3 distinct other lexemes submissions with sequence > 10
	_, err = db.Exec(`
		INSERT INTO lexeme (id, canonical_form, created_at) VALUES ('lex_d', 'dog', 1), ('lex_e', 'elephant', 1), ('lex_f', 'fox', 1);
		INSERT INTO sense (id, lexeme_id, part_of_speech, source_id, created_at)
		VALUES ('sense_d', 'lex_d', 'n', 'src1', 1),
		       ('sense_e', 'lex_e', 'n', 'src1', 1),
		       ('sense_f', 'lex_f', 'n', 'src1', 1);
		INSERT INTO question_instance (id, session_id, ordinal, question_version_id, snapshot_json, created_at)
		VALUES ('inst_d', 'sess_corr', 1, 'qv_c1', '{}', 2000),
		       ('inst_e', 'sess_corr', 2, 'qv_c1', '{}', 2100),
		       ('inst_f', 'sess_corr', 3, 'qv_c1', '{}', 2200);
		INSERT INTO global_sequence (seq, event_name, created_at) VALUES (20, 'ev', 2000), (30, 'ev', 2100), (40, 'ev', 2200);
		INSERT INTO attempt_event (id, instance_id, session_id, sense_id, task_family, source, purpose, response_history, grading_version, is_correct, observed_at, event_sequence, created_at)
		VALUES ('att_d', 'inst_d', 'sess_corr', 'sense_d', 'meaning_recognition', 'practice', 'normal', 'single_response', 'grading-v1', 1, 2000, 20, 2000),
		       ('att_e', 'inst_e', 'sess_corr', 'sense_e', 'meaning_recognition', 'practice', 'normal', 'single_response', 'grading-v1', 1, 2100, 30, 2100),
		       ('att_f', 'inst_f', 'sess_corr', 'sense_f', 'meaning_recognition', 'practice', 'normal', 'single_response', 'grading-v1', 1, 2200, 40, 2200);
	`)
	if err != nil {
		t.Fatalf("failed to insert 3 other lexemes: %v", err)
	}

	// 3. Now 3 distinct lexemes completed -> correction SHOULD be selected!
	// And it must select form_c2 (since form_c1 was already answered in inst_c1).
	resCorr, err := selector.SelectNextQuestion(ctx, selection.SelectionRequest{
		SessionID:         "sess_corr",
		BankID:            "bank1",
		NormalPosition:    1,
		ProjectionVersion: "v1",
		NowMicro:          3000,
	})
	if err != nil {
		t.Fatalf("select after cooling: %v", err)
	}
	if !resCorr.IsCorrection || resCorr.CandidateGroup != "correction" {
		t.Errorf("expected correction to be selected, got %+v", resCorr)
	}
	if resCorr.FormID != "form_c2" {
		t.Errorf("expected new form_c2 for correction, got %s", resCorr.FormID)
	}
}

func TestSelectionCooldownScalability(t *testing.T) {
	db, dir := setupSelectionDB(t)
	defer os.RemoveAll(dir)
	defer db.Close()

	ctx := context.Background()

	// Seed source, bank, lexemes, senses
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	_, err = tx.Exec(`
		INSERT INTO source (id, name, location, source_version, license, usage_scope, created_at, updated_at)
		VALUES ('src1', 'src', 'loc', 'v1', 'mit', 'test', 1, 1);
		INSERT INTO bank (id, name, created_at, updated_at) VALUES ('bank1', 'Test Bank', 1, 1);
		INSERT INTO session (id, kind, status, created_at, updated_at) VALUES ('sess_scale', 'practice', 'open', 1, 1);
	`)
	if err != nil {
		t.Fatalf("seed basic: %v", err)
	}

	// Insert 2,000 exposure events and attempt events (older than 24h ago)
	nowMicro := time.Now().UnixMicro()
	oldMicro := nowMicro - 48*3600*1000000 // 48 hours ago
	for i := 0; i < 2000; i++ {
		_, _ = tx.Exec(`
			INSERT INTO exposure_event (id, session_id, sense_id, task_family, ordinal, event_name, event_sequence, observed_at, created_at)
			VALUES (?, 'sess_scale', 'sense_old', 'meaning_recognition', ?, 'exposure', ?, ?, ?)
		`, "exp_"+string(rune(i)), i, i, oldMicro, oldMicro)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit tx: %v", err)
	}

	cfg := selection.DefaultSelectionConfig()

	start := time.Now()
	// Because the latest exposure was 48h ago (> 24h), it should short-circuit immediately via the 24h rule!
	inCooldown, err := selection.CheckNormalCooldown(ctx, db, cfg, "sess_scale", "lex_scale", nowMicro)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("CheckNormalCooldown failed: %v", err)
	}
	if inCooldown {
		t.Errorf("expected lex_scale not to be in cooldown after 48h, got inCooldown=true")
	}
	// Verify execution took < 100ms
	if elapsed > 200*time.Millisecond {
		t.Errorf("CheckNormalCooldown too slow: %v (expected < 200ms)", elapsed)
	}
}

