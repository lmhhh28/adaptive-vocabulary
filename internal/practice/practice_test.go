package practice

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"adaptive-vocabulary/internal/content"
	"adaptive-vocabulary/internal/core"
	"adaptive-vocabulary/internal/learning"
	"adaptive-vocabulary/internal/selection"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) (*sql.DB, *Service, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "avocab-practice-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(dir, "app.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)

	if _, err := core.RunMigrations(db); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	ctx := context.Background()
	if err := content.ApplySeed(ctx, db, "core"); err != nil {
		t.Fatalf("failed to apply seed core: %v", err)
	}

	policyCfg, err := learning.LoadFSRSPolicyFromPath("../../config/policy-v2.json")
	if err != nil {
		policyCfg, err = learning.LoadFSRSPolicyFromPath("config/policy-v2.json")
		if err != nil {
			t.Fatalf("failed to load fsrs policy: %v", err)
		}
	}

	selCfg, err := selection.LoadSelectionConfigFromPath("../../config/policy-v2.json")
	if err != nil {
		selCfg, err = selection.LoadSelectionConfigFromPath("config/policy-v2.json")
		if err != nil {
			def := selection.DefaultSelectionConfig()
			selCfg = &def
		}
	}

	learningSvc := learning.NewLearningService(db, *policyCfg, "observation-v2", "v1")
	selector := selection.NewSelector(db, *selCfg)
	service := NewService(db, learningSvc, selector)

	cleanup := func() {
		db.Close()
		os.RemoveAll(dir)
	}

	return db, service, cleanup
}

func TestCreateAndResumeSession(t *testing.T) {
	_, svc, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	bankID := "00000000-0000-4000-8000-000000000801"

	// 1. Create practice session
	sess1, isResumed, err := svc.CreateOrResumeSession(ctx, bankID, "", "")
	if err != nil {
		t.Fatalf("unexpected error creating session: %v", err)
	}
	if isResumed {
		t.Fatalf("expected new session, got resumed")
	}
	if sess1.ID == "" || sess1.Kind != "practice" || sess1.Status != "open" {
		t.Fatalf("invalid session state: %+v", sess1)
	}

	// 2. Try creating session for same bank -> resumes existing session
	sess2, isResumed2, err := svc.CreateOrResumeSession(ctx, bankID, "", "")
	if err != nil {
		t.Fatalf("unexpected error resuming session: %v", err)
	}
	if !isResumed2 {
		t.Fatalf("expected session to be resumed")
	}
	if sess2.ID != sess1.ID {
		t.Fatalf("expected same session ID %s, got %s", sess1.ID, sess2.ID)
	}

	// 3. Try creating session for different bank -> conflict
	otherBankID := "00000000-0000-4000-8000-000000000802"
	_, _, err = svc.CreateOrResumeSession(ctx, otherBankID, "", "")
	if err == nil {
		t.Fatalf("expected ActiveSessionConflictError, got nil")
	}
}

func TestPracticeFullLifecycle(t *testing.T) {
	_, svc, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	bankID := "00000000-0000-4000-8000-000000000801"

	// 1. Start session
	sess, _, err := svc.CreateOrResumeSession(ctx, bankID, "idemp-create", "digest1")
	if err != nil {
		t.Fatalf("create session error: %v", err)
	}

	// 2. Next question (cursor 0)
	nextRes, err := svc.NextQuestion(ctx, sess.ID, 0)
	if err != nil {
		t.Fatalf("next question error: %v", err)
	}
	if nextRes.Status != "ready" || nextRes.Question == nil {
		t.Fatalf("expected ready question, got: %+v", nextRes)
	}
	if nextRes.Question.Ordinal != 0 {
		t.Fatalf("expected ordinal 0, got %d", nextRes.Question.Ordinal)
	}
	if len(nextRes.Question.Options) != 4 {
		t.Fatalf("expected 4 options, got %d", len(nextRes.Question.Options))
	}

	// Calling next again with cursor 0 before answering should return the SAME unanswered instance
	nextResRepeat, err := svc.NextQuestion(ctx, sess.ID, 0)
	if err != nil {
		t.Fatalf("repeat next question error: %v", err)
	}
	if nextResRepeat.Question.InstanceID != nextRes.Question.InstanceID {
		t.Fatalf("expected same instance ID on repeat next, got %s vs %s", nextResRepeat.Question.InstanceID, nextRes.Question.InstanceID)
	}

	// 3. Submit attempt
	firstOption := nextRes.Question.Options[0]
	fb, isReplayed, err := svc.SubmitAttempt(ctx, sess.ID, SubmitAttemptRequest{
		InstanceID: nextRes.Question.InstanceID,
		OptionID:   firstOption.ID,
	}, "idemp-sub-1", "digest-sub-1", "")
	if err != nil {
		t.Fatalf("submit attempt error: %v", err)
	}
	if isReplayed {
		t.Fatalf("expected not replayed on first submit")
	}
	if fb.NextCursor != 1 {
		t.Fatalf("expected next_cursor 1, got %d", fb.NextCursor)
	}

	// Resubmitting same option should replay feedback
	fbRepeat, _, err := svc.SubmitAttempt(ctx, sess.ID, SubmitAttemptRequest{
		InstanceID: nextRes.Question.InstanceID,
		OptionID:   firstOption.ID,
	}, "idemp-sub-1", "digest-sub-1", "")
	if err != nil {
		t.Fatalf("repeat submit error: %v", err)
	}
	if fbRepeat.AttemptID != fb.AttemptID {
		t.Fatalf("expected same attempt ID %s, got %s", fb.AttemptID, fbRepeat.AttemptID)
	}

	// Submitting DIFFERENT option for same instance should conflict
	if len(nextRes.Question.Options) > 1 {
		diffOption := nextRes.Question.Options[1]
		_, _, err = svc.SubmitAttempt(ctx, sess.ID, SubmitAttemptRequest{
			InstanceID: nextRes.Question.InstanceID,
			OptionID:   diffOption.ID,
		}, "idemp-sub-diff", "digest-sub-diff", "")
		if err == nil {
			t.Fatalf("expected already answered conflict error, got nil")
		}
	}

	// 4. End session
	endRes, err := svc.EndSession(ctx, sess.ID, &fb.SessionRevision, "ended")
	if err != nil {
		t.Fatalf("end session error: %v", err)
	}
	if endRes.Session.Status != "ended" {
		t.Fatalf("expected status 'ended', got %s", endRes.Session.Status)
	}

	// Calling next on ended session should fail with ErrSessionClosed
	_, err = svc.NextQuestion(ctx, sess.ID, 1)
	if !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("expected ErrSessionClosed, got %v", err)
	}
}
