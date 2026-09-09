package learning_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"adaptive-vocabulary/internal/core"
	"adaptive-vocabulary/internal/learning"
	"adaptive-vocabulary/internal/platform"
)

func setupTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "avocab-learning-test-")
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

func TestEligibilityMatrix(t *testing.T) {
	// Normal practice
	att1 := &learning.AttemptEvent{Source: learning.SourcePractice, Purpose: learning.PurposeNormal}
	dec1 := learning.ClassifyEligibility(att1, false)
	if !dec1.OrdinaryEvidence || !dec1.OrdinaryConfusion || !dec1.UpdateConsolidation || !dec1.NormalGap || !dec1.CorrectionGap || !dec1.FSRSReview {
		t.Errorf("practice_normal mismatch: %+v", dec1)
	}

	// Early review practice
	att2 := &learning.AttemptEvent{Source: learning.SourcePractice, Purpose: learning.PurposeEarlyReview}
	dec2 := learning.ClassifyEligibility(att2, false)
	if !dec2.OrdinaryEvidence || !dec2.OrdinaryConfusion || !dec2.UpdateConsolidation || !dec2.NormalGap || !dec2.CorrectionGap || !dec2.FSRSReview {
		t.Errorf("practice_early_review mismatch: %+v", dec2)
	}

	// Correction practice: does NOT advance normal gap, does NOT update consolidation, not ordinary evidence
	att3 := &learning.AttemptEvent{Source: learning.SourcePractice, Purpose: learning.PurposeCorrection}
	dec3 := learning.ClassifyEligibility(att3, false)
	if dec3.OrdinaryEvidence || dec3.OrdinaryConfusion || dec3.UpdateConsolidation || dec3.NormalGap || !dec3.CorrectionGap || !dec3.FSRSReview {
		t.Errorf("practice_correction mismatch: %+v", dec3)
	}

	// Exam single response: ordinary evidence & FSRS, but does NOT advance normal/correction gap
	att4 := &learning.AttemptEvent{Source: learning.SourceExam, ResponseHistory: learning.ResponseSingleResponse}
	dec4 := learning.ClassifyEligibility(att4, false)
	if !dec4.OrdinaryEvidence || !dec4.OrdinaryConfusion || !dec4.UpdateConsolidation || dec4.NormalGap || dec4.CorrectionGap || !dec4.FSRSReview {
		t.Errorf("exam_single_response mismatch: %+v", dec4)
	}

	// Exam revised: no FSRS review, no ordinary evidence
	att5 := &learning.AttemptEvent{Source: learning.SourceExam, ResponseHistory: learning.ResponseRevised}
	dec5 := learning.ClassifyEligibility(att5, false)
	if dec5.OrdinaryEvidence || dec5.FSRSReview {
		t.Errorf("exam_revised mismatch: %+v", dec5)
	}
}

func TestExposureDerivationCutoff(t *testing.T) {
	db, dir := setupTestDB(t)
	defer os.RemoveAll(dir)
	defer db.Close()

	ctx := context.Background()

	// Seed minimal entries
	_, err := db.Exec(`
		INSERT INTO source (id, name, location, source_version, license, usage_scope, created_at, updated_at)
		VALUES ('src1', 'src', 'loc', 'v1', 'mit', 'test', 1, 1);
		INSERT INTO lexeme (id, canonical_form, created_at) VALUES ('lex_river', 'river', 1);
		INSERT INTO sense (id, lexeme_id, part_of_speech, source_id, created_at) VALUES ('sense_river', 'lex_river', 'n', 'src1', 1);
		INSERT INTO sense_version (id, sense_id, meaning, created_at) VALUES ('sv_river', 'sense_river', '河流', 1);
		INSERT INTO question_form (id, sense_id, task_family, dedup_hash, normalized_stem, created_at)
		VALUES ('form_river_1', 'sense_river', 'meaning_recognition', 'hash1', 'river stem 1', 1),
		       ('form_river_2', 'sense_river', 'meaning_recognition', 'hash2', 'river stem 2', 1);
		INSERT INTO question_version (id, form_id, sense_id, sense_version_id, task_family, stem, options_json, correct_option_id, explanation, created_at, updated_at)
		VALUES ('qv_river_1', 'form_river_1', 'sense_river', 'sv_river', 'meaning_recognition', 'river stem 1', '[]', 'opt1', 'exp', 1, 1),
		       ('qv_river_2', 'form_river_2', 'sense_river', 'sv_river', 'meaning_recognition', 'river stem 2', '[]', 'opt2', 'exp', 1, 1);
		INSERT INTO session (id, kind, status, created_at, updated_at) VALUES ('sess1', 'practice', 'open', 1, 1);
		INSERT INTO question_instance (id, session_id, ordinal, question_version_id, snapshot_json, created_at)
		VALUES ('inst1', 'sess1', 0, 'qv_river_1', '{}', 1000000),
		       ('inst2', 'sess1', 1, 'qv_river_1', '{}', 2000000),
		       ('inst3', 'sess1', 2, 'qv_river_2', '{}', 3000000);
	`)
	if err != nil {
		t.Fatalf("failed to insert test fixtures: %v", err)
	}

	// 1. Initial state: inst1 has reserved exposure at t=1000000
	err = core.RunInTx(ctx, db, func(tc *core.TxnContext) error {
		return learning.RecordExposure(tc, &learning.ExposureEvent{
			SourceInstanceID: "inst1",
			Kind:             learning.ExposureReserved,
			SenseID:          "sense_river",
			LexemeID:         "lex_river",
			FormID:           "form_river_1",
			ObservedAt:       1000000,
		})
	})
	if err != nil {
		t.Fatalf("record exposure 1: %v", err)
	}

	// Check inst1 exposure labels (should exclude itself)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	hasSameForm, hasRecentRelated, err := learning.DeriveExposureLabels(ctx, tx, "inst1", "form_river_1", "lex_river", 1000000, 100)
	if err != nil {
		t.Fatal(err)
	}
	if hasSameForm || hasRecentRelated {
		t.Errorf("expected inst1 to have no historical labels (excludes itself), got sameForm=%v, recent=%v", hasSameForm, hasRecentRelated)
	}

	// 2. inst2 (different instance, same form) at t=2000000 (within 24h)
	hasSameForm, hasRecentRelated, err = learning.DeriveExposureLabels(ctx, tx, "inst2", "form_river_1", "lex_river", 2000000, 200)
	if err != nil {
		t.Fatal(err)
	}
	if !hasSameForm || !hasRecentRelated {
		t.Errorf("expected inst2 to have both sameForm and recentRelated, got sameForm=%v, recent=%v", hasSameForm, hasRecentRelated)
	}

	// 3. inst3 (different form, same lexeme) at t=3000000 (within 24h)
	hasSameForm, hasRecentRelated, err = learning.DeriveExposureLabels(ctx, tx, "inst3", "form_river_2", "lex_river", 3000000, 300)
	if err != nil {
		t.Fatal(err)
	}
	if hasSameForm {
		t.Errorf("expected inst3 to not have sameForm, got true")
	}
	if !hasRecentRelated {
		t.Errorf("expected inst3 to have recentRelated, got false")
	}

	// 4. inst4 after 24h + 1 second (t = 1000000 + 24*3600*1000000 + 1000000)
	tFar := int64(1000000) + 24*3600*1000000 + 1000000
	hasSameForm, hasRecentRelated, err = learning.DeriveExposureLabels(ctx, tx, "inst4", "form_river_1", "lex_river", tFar, 400)
	if err != nil {
		t.Fatal(err)
	}
	if !hasSameForm {
		t.Errorf("expected sameForm to persist across 24h, got false")
	}
	if hasRecentRelated {
		t.Errorf("expected recentRelated to expire after 24h, got true")
	}
}

func TestApplyAttemptAndReplay(t *testing.T) {
	db, dir := setupTestDB(t)
	defer os.RemoveAll(dir)
	defer db.Close()

	ctx := context.Background()

	// Seed basic data
	_, err := db.Exec(`
		INSERT INTO source (id, name, location, source_version, license, usage_scope, created_at, updated_at)
		VALUES ('src1', 'src', 'loc', 'v1', 'mit', 'test', 1, 1);
		INSERT INTO lexeme (id, canonical_form, created_at) VALUES ('lex_bank', 'bank', 1);
		INSERT INTO sense (id, lexeme_id, part_of_speech, source_id, created_at) VALUES ('sense_bank', 'lex_bank', 'n', 'src1', 1);
		INSERT INTO sense_version (id, sense_id, meaning, created_at) VALUES ('sv_bank', 'sense_bank', '银行', 1);
		INSERT INTO option_identity (id, canonical_key, language, normalized_text, sense_id, source_id, created_at)
		VALUES ('dist1', 'key1', 'zh', '河流', 'sense_bank', 'src1', 1),
		       ('dist2', 'key2', 'zh', '山峰', 'sense_bank', 'src1', 1);
		INSERT INTO question_form (id, sense_id, task_family, dedup_hash, normalized_stem, created_at)
		VALUES ('form_bank_1', 'sense_bank', 'meaning_recognition', 'hash_b1', 'bank stem', 1);
		INSERT INTO question_version (id, form_id, sense_id, sense_version_id, task_family, stem, options_json, correct_option_id, explanation, created_at, updated_at)
		VALUES ('qv_bank_1', 'form_bank_1', 'sense_bank', 'sv_bank', 'meaning_recognition', 'bank stem', '[]', 'opt_corr', 'exp', 1, 1);
		INSERT INTO session (id, kind, status, created_at, updated_at) VALUES ('sess_prac', 'practice', 'open', 1, 1);
		INSERT INTO question_instance (id, session_id, ordinal, question_version_id, snapshot_json, created_at)
		VALUES ('inst_b1', 'sess_prac', 0, 'qv_bank_1', '{"options":[{"id":"opt_corr","option_identity_id":"corr1","is_correct":true},{"id":"opt_w1","option_identity_id":"dist1","is_correct":false},{"id":"opt_w2","option_identity_id":"dist2","is_correct":false}],"correct_option_id":"opt_corr"}', 1000000);
	`)
	if err != nil {
		t.Fatalf("failed to insert test fixtures: %v", err)
	}

	policyCandidates := []string{
		"config/policy-v2.json",
		"../config/policy-v2.json",
		"../../config/policy-v2.json",
	}
	var policyCfg *learning.FSRSPolicyConfig
	for _, p := range policyCandidates {
		if cfg, err := learning.LoadFSRSPolicyFromPath(p); err == nil {
			policyCfg = cfg
			break
		}
	}
	if policyCfg == nil {
		t.Fatalf("could not find config/policy-v2.json in %v", policyCandidates)
	}

	service := learning.NewLearningService(db, *policyCfg, "observation-v2", "v1")

	// Apply 1: Normal attempt wrong -> sets needs_consolidation=1
	optWrong := "opt_w1"
	err = core.RunInTx(ctx, db, func(tc *core.TxnContext) error {
		_, err := service.ApplyAttempt(tc, &learning.AttemptEvent{
			InstanceID:       "inst_b1",
			SessionID:        "sess_prac",
			SenseID:          "sense_bank",
			TaskFamily:       learning.TaskMeaningRecognition,
			Source:           learning.SourcePractice,
			Purpose:          learning.PurposeNormal,
			ResponseHistory:  learning.ResponseSingleResponse,
			SelectedOptionID: &optWrong,
			GradingVersion:   "grading-v1",
			IsCorrect:        false,
			ObservedAt:       1000000,
		}, []learning.InstanceOption{
			{ID: "opt_corr", OptionIdentityID: "corr1", IsCorrect: true},
			{ID: "opt_w1", OptionIdentityID: "dist1", IsCorrect: false},
			{ID: "opt_w2", OptionIdentityID: "dist2", IsCorrect: false},
		})
		return err
	})
	if err != nil {
		t.Fatalf("apply attempt 1: %v", err)
	}

	// Check evidence projection
	var needsC int
	var ordSamples int
	err = db.QueryRow("SELECT needs_consolidation, ordinary_samples FROM evidence_projection WHERE projection_version = 'v1' AND sense_id = 'sense_bank'").Scan(&needsC, &ordSamples)
	if err != nil {
		t.Fatalf("query evidence: %v", err)
	}
	if needsC != 1 || ordSamples != 1 {
		t.Errorf("expected needs_consolidation=1, ordSamples=1, got needsC=%d, ordS=%d", needsC, ordSamples)
	}

	// Check confusion projection
	var offered, wrong int
	err = db.QueryRow("SELECT eligible_offered_count, wrong_selected_count FROM confusion_projection WHERE projection_version = 'v1' AND distractor_identity_id = 'dist1'").Scan(&offered, &wrong)
	if err != nil {
		t.Fatalf("query confusion: %v", err)
	}
	if offered != 1 || wrong != 1 {
		t.Errorf("expected offered=1, wrong=1 for dist1, got offered=%d, wrong=%d", offered, wrong)
	}

	// Check correction item created
	var corrStatus string
	var retries int
	err = db.QueryRow("SELECT status, retries_count FROM correction_item WHERE session_id = 'sess_prac' AND sense_id = 'sense_bank'").Scan(&corrStatus, &retries)
	if err != nil {
		t.Fatalf("query correction_item: %v", err)
	}
	if corrStatus != "pending" || retries != 0 {
		t.Errorf("expected pending correction with 0 retries, got status=%s, retries=%d", corrStatus, retries)
	}

	// Replay to version v2
	err = service.ReplayProjections(ctx, "v2", 0)
	if err != nil {
		t.Fatalf("replay error: %v", err)
	}

	identical, diff, err := learning.CompareProjections(ctx, db, "v1", "v2")
	if err != nil {
		t.Fatalf("compare error: %v", err)
	}
	if !identical {
		t.Fatalf("projections not identical: %s", diff)
	}
}
