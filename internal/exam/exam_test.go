package exam_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"adaptive-vocabulary/internal/content"
	"adaptive-vocabulary/internal/core"
	"adaptive-vocabulary/internal/exam"
	"adaptive-vocabulary/internal/learning"
	"adaptive-vocabulary/internal/platform"
)

func setupTestDB(t *testing.T, seed string) (*sql.DB, *exam.Service) {
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

	if seed != "" {
		if err := content.ApplySeed(context.Background(), db.DB, seed); err != nil {
			t.Fatalf("ApplySeed %s failed: %v", seed, err)
		}
	}

	policyPath := filepath.Join("..", "..", "config", "policy-v2.json")
	fsrsPolicy, err := learning.LoadFSRSPolicyFromPath(policyPath)
	if err != nil {
		t.Fatalf("LoadFSRSPolicyFromPath failed: %v", err)
	}

	learningSvc := learning.NewLearningService(db.DB, *fsrsPolicy, "observation-v2", "v1")
	assembler := exam.NewAssembler(db.DB, 20000)
	svc := exam.NewService(db.DB, learningSvc, assembler)

	return db.DB, svc
}

func TestResolveQuotas(t *testing.T) {
	// Even split
	q, err := exam.ResolveQuotas(3, nil)
	if err != nil {
		t.Fatalf("ResolveQuotas(3, nil) failed: %v", err)
	}
	if q["meaning_recognition"] != 1 || q["reverse_selection"] != 1 || q["context_selection"] != 1 {
		t.Errorf("expected 1 of each, got %+v", q)
	}

	// User quotas matching
	userQ := map[string]int{
		"meaning_recognition": 2,
		"reverse_selection":   1,
	}
	q2, err := exam.ResolveQuotas(3, userQ)
	if err != nil {
		t.Fatalf("ResolveQuotas matching failed: %v", err)
	}
	if q2["meaning_recognition"] != 2 || q2["reverse_selection"] != 1 || q2["context_selection"] != 0 {
		t.Errorf("unexpected quotas: %+v", q2)
	}

	// Mismatched total
	_, err = exam.ResolveQuotas(4, userQ)
	if err == nil {
		t.Error("expected error for mismatched quota total")
	}

	// Unknown family
	_, err = exam.ResolveQuotas(1, map[string]int{"foo": 1})
	if err == nil {
		t.Error("expected error for unknown family")
	}
}

func TestExamAssemblyCoreSuccess(t *testing.T) {
	_, svc := setupTestDB(t, "core")

	resp, _, err := svc.CreateExam(context.Background(), exam.CreateExamRequest{
		BankID:        "00000000-0000-4000-8000-000000000801",
		QuestionCount: 3,
	}, "test-key-1", "digest-1")
	if err != nil {
		t.Fatalf("CreateExam failed: %v", err)
	}

	if len(resp.PublicQuestions) != 3 {
		t.Fatalf("expected 3 questions, got %d", len(resp.PublicQuestions))
	}
	if len(resp.Drafts) != 3 {
		t.Fatalf("expected 3 drafts, got %d", len(resp.Drafts))
	}

	tasks := make(map[string]int)
	for _, q := range resp.PublicQuestions {
		tasks[q.TaskFamily]++
		if len(q.Options) != 4 {
			t.Errorf("expected 4 options, got %d", len(q.Options))
		}
	}
	if tasks["meaning_recognition"] != 1 || tasks["reverse_selection"] != 1 || tasks["context_selection"] != 1 {
		t.Errorf("expected balanced task families, got %+v", tasks)
	}
}

func TestExamAssemblyConflictUnsatisfied(t *testing.T) {
	_, svc := setupTestDB(t, "exam-conflict")

	_, _, err := svc.CreateExam(context.Background(), exam.CreateExamRequest{
		BankID:        "00000000-0000-4000-8000-000000000801",
		QuestionCount: 3,
	}, "test-key-conflict", "digest-c")
	if err == nil {
		t.Fatal("expected error for conflict assembly, got nil")
	}
	if !errors.Is(err, exam.ErrConstraintUnsatisfied) {
		t.Errorf("expected ErrConstraintUnsatisfied, got: %v", err)
	}
}

func TestExamResponseEventFlowAndRebase(t *testing.T) {
	_, svc := setupTestDB(t, "core")

	createResp, _, err := svc.CreateExam(context.Background(), exam.CreateExamRequest{
		BankID:        "00000000-0000-4000-8000-000000000801",
		QuestionCount: 3,
	}, "idemp-create", "dig-1")
	if err != nil {
		t.Fatalf("CreateExam failed: %v", err)
	}

	examID := createResp.ExamID
	q0 := createResp.PublicQuestions[0]
	opt0 := q0.Options[0].ID
	opt1 := q0.Options[1].ID

	// 1. First event: select opt0
	ev1, _, err := svc.RecordResponseEvent(context.Background(), examID, exam.ResponseEventRequest{
		InstanceID:       q0.InstanceID,
		Action:           "select",
		OptionID:         &opt0,
		ClientEventID:    core.NewUUID(),
		ClientSequence:   1,
		ExpectedRevision: 0,
	}, "idemp-ev1", "dig-ev1")
	if err != nil {
		t.Fatalf("RecordResponseEvent ev1 failed: %v", err)
	}
	if ev1.AcceptedSequence != 1 || ev1.CurrentRevision != 1 {
		t.Errorf("ev1 unexpected seq/rev: seq=%d rev=%d", ev1.AcceptedSequence, ev1.CurrentRevision)
	}

	// 2. Conflict: sending with old expected_revision=0 -> must return RevisionConflictNotAcceptedError
	evID2 := core.NewUUID()
	_, _, err = svc.RecordResponseEvent(context.Background(), examID, exam.ResponseEventRequest{
		InstanceID:       q0.InstanceID,
		Action:           "select",
		OptionID:         &opt1,
		ClientEventID:    evID2,
		ClientSequence:   2,
		ExpectedRevision: 0, // stale!
	}, evID2, "dig-ev2")
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	var revErr *exam.RevisionConflictNotAcceptedError
	if !errors.As(err, &revErr) {
		t.Fatalf("expected RevisionConflictNotAcceptedError, got: %v", err)
	}
	if revErr.CurrentRevision != 1 || revErr.LastAckSequence != 1 {
		t.Errorf("unexpected revErr values: %+v", revErr)
	}

	// Receipt for evID2 should be 404
	_, err = svc.GetResponseEventReceipt(context.Background(), examID, evID2)
	if err == nil {
		t.Error("expected receipt 404 for rejected event")
	}

	// 3. Rebase: re-send with expected_revision=1, client_sequence=2
	ev2Rebased, _, err := svc.RecordResponseEvent(context.Background(), examID, exam.ResponseEventRequest{
		InstanceID:       q0.InstanceID,
		Action:           "select",
		OptionID:         &opt1,
		ClientEventID:    evID2,
		ClientSequence:   2,
		ExpectedRevision: 1,
		RebasedFrom: &exam.RebasedFromDTO{
			ClientSequence:   2,
			ExpectedRevision: 0,
		},
	}, evID2, "dig-ev2-rebased")
	if err != nil {
		t.Fatalf("Rebased RecordResponseEvent failed: %v", err)
	}
	if ev2Rebased.AcceptedSequence != 2 || ev2Rebased.CurrentRevision != 2 {
		t.Errorf("ev2Rebased unexpected seq/rev: seq=%d rev=%d", ev2Rebased.AcceptedSequence, ev2Rebased.CurrentRevision)
	}
	if ev2Rebased.Draft.RevisionCount != 1 {
		t.Errorf("expected revision_count=1, got %d", ev2Rebased.Draft.RevisionCount)
	}

	// 4. Receipt for evID2 should now exist
	receipt, err := svc.GetResponseEventReceipt(context.Background(), examID, evID2)
	if err != nil {
		t.Fatalf("GetResponseEventReceipt failed: %v", err)
	}
	if receipt.ClientEventID != evID2 || receipt.AcceptedRevision != 2 {
		t.Errorf("unexpected receipt: %+v", receipt)
	}
}

func TestExamSubmitAndScoring(t *testing.T) {
	_, svc := setupTestDB(t, "core")

	createResp, _, err := svc.CreateExam(context.Background(), exam.CreateExamRequest{
		BankID:        "00000000-0000-4000-8000-000000000801",
		QuestionCount: 3,
	}, "idemp-create-sub", "dig-sub")
	if err != nil {
		t.Fatalf("CreateExam failed: %v", err)
	}
	examID := createResp.ExamID

	// Answer question 0 with first option
	q0 := createResp.PublicQuestions[0]
	opt0 := q0.Options[0].ID
	evID := core.NewUUID()
	_, _, err = svc.RecordResponseEvent(context.Background(), examID, exam.ResponseEventRequest{
		InstanceID:       q0.InstanceID,
		Action:           "select",
		OptionID:         &opt0,
		ClientEventID:    evID,
		ClientSequence:   1,
		ExpectedRevision: 0,
	}, evID, "dig-ans")
	if err != nil {
		t.Fatalf("RecordResponseEvent failed: %v", err)
	}

	// Submit exam
	subResp, _, err := svc.SubmitExam(context.Background(), examID, exam.SubmitExamRequest{
		ExpectedRevision: 1,
		SubmissionID:     "sub-123",
		LastAckSequence:  1,
	}, "sub-key", "sub-dig")
	if err != nil {
		t.Fatalf("SubmitExam failed: %v", err)
	}
	if subResp.Status != "submitted" {
		t.Errorf("expected status=submitted, got %s", subResp.Status)
	}

	// Get result
	res, err := svc.GetExamResult(context.Background(), examID)
	if err != nil {
		t.Fatalf("GetExamResult failed: %v", err)
	}
	if res.Total != 3 {
		t.Errorf("expected total=3, got %d", res.Total)
	}
	if res.Omitted != 2 {
		t.Errorf("expected omitted=2, got %d", res.Omitted)
	}
}

func TestExamExpiredDeadlineSettle(t *testing.T) {
	db, svc := setupTestDB(t, "core")

	tSec := 60
	createResp, _, err := svc.CreateExam(context.Background(), exam.CreateExamRequest{
		BankID:           "00000000-0000-4000-8000-000000000801",
		QuestionCount:    3,
		TimeLimitSeconds: &tSec,
	}, "idemp-exp", "dig-exp")
	if err != nil {
		t.Fatalf("CreateExam failed: %v", err)
	}
	examID := createResp.ExamID

	// Force deadline to the past in database
	pastMicro := time.Now().Add(-10 * time.Minute).UTC().UnixNano() / 1000
	_, err = db.Exec("UPDATE session SET deadline_at = ? WHERE id = ?", pastMicro, examID)
	if err != nil {
		t.Fatalf("update deadline failed: %v", err)
	}
	_, err = db.Exec("UPDATE exam_session SET deadline_at = ? WHERE session_id = ?", pastMicro, examID)
	if err != nil {
		t.Fatalf("update exam_session deadline failed: %v", err)
	}

	// Run SettleExpiredExams
	settled, err := svc.SettleExpiredExams(context.Background())
	if err != nil {
		t.Fatalf("SettleExpiredExams failed: %v", err)
	}
	if settled != 1 {
		t.Errorf("expected 1 settled exam, got %d", settled)
	}

	// Verify exam is now submitted
	detail, err := svc.GetExam(context.Background(), examID)
	if err != nil {
		t.Fatalf("GetExam failed: %v", err)
	}
	if detail.Status != "submitted" {
		t.Errorf("expected status=submitted, got %s", detail.Status)
	}
}

func TestAbandonExamBodyValidation(t *testing.T) {
	db, _ := setupTestDB(t, "core")

	policyPath := filepath.Join("..", "..", "config", "policy-v2.json")
	handler, err := exam.NewHandler(db, policyPath)
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	ctx := context.Background()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	bankID := "00000000-0000-4000-8000-000000000801"

	// Create exam
	createResp, _, err := handler.Service().CreateExam(ctx, exam.CreateExamRequest{
		BankID:        bankID,
		QuestionCount: 2,
	}, "", "")
	if err != nil {
		t.Fatalf("create exam: %v", err)
	}
	examID := createResp.ExamID

	// Test invalid request bodies
	invalidBodies := []string{"", "{", "{}"}
	for _, body := range invalidBodies {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/v1/exams/"+examID+"/abandon", strings.NewReader(body))
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for body %q, got %d", body, w.Code)
		}

		// Ensure session is still open
		var status string
		if err := db.QueryRow("SELECT status FROM session WHERE id = ?", examID).Scan(&status); err != nil {
			t.Fatalf("query status: %v", err)
		}
		if status != "open" {
			t.Errorf("expected session to remain open for body %q, got %s", body, status)
		}
	}

	// Test valid body with expected_revision=0
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/exams/"+examID+"/abandon", strings.NewReader(`{"expected_revision": 0}`))
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for valid abandon, got %d", w.Code)
	}
	var status string
	if err := db.QueryRow("SELECT status FROM session WHERE id = ?", examID).Scan(&status); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "abandoned" {
		t.Errorf("expected session to be abandoned, got %s", status)
	}
}

func TestRecordTelemetryGapIdempotentReplayAfterClosure(t *testing.T) {
	_, svc := setupTestDB(t, "core")

	ctx := context.Background()
	bankID := "00000000-0000-4000-8000-000000000801"

	// Create exam
	createResp, _, err := svc.CreateExam(ctx, exam.CreateExamRequest{
		BankID:        bankID,
		QuestionCount: 2,
	}, "", "")
	if err != nil {
		t.Fatalf("create exam: %v", err)
	}
	examID := createResp.ExamID

	// Record telemetry gap with idempotency
	key := "gap-key-1"
	digest := "gap-digest-1"
	gapResp, err := svc.RecordTelemetryGap(ctx, examID, exam.TelemetryGapsRequest{
		ExpectedRevision: 0,
		Reason:           "lost_queue",
	}, key, digest)
	if err != nil {
		t.Fatalf("record telemetry gap: %v", err)
	}

	// Abandon the exam
	_, err = svc.AbandonExam(ctx, examID, exam.AbandonExamRequest{
		ExpectedRevision: gapResp.CurrentRevision,
	})
	if err != nil {
		t.Fatalf("abandon exam: %v", err)
	}

	// Replay the same telemetry gap after closure
	replayedGap, err := svc.RecordTelemetryGap(ctx, examID, exam.TelemetryGapsRequest{
		ExpectedRevision: 0,
		Reason:           "lost_queue",
	}, key, digest)
	if err != nil {
		t.Fatalf("expected idempotent replay to succeed after closure, got: %v", err)
	}
	if replayedGap.CurrentRevision != gapResp.CurrentRevision {
		t.Errorf("expected current revision %d, got %d", gapResp.CurrentRevision, replayedGap.CurrentRevision)
	}
}

func TestCreateExamMaxQuestionsCap(t *testing.T) {
	_, svc := setupTestDB(t, "core")
	ctx := context.Background()
	bankID := "00000000-0000-4000-8000-000000000801"

	// Requesting question_count = 201 should be rejected when maxQuestions is 200
	_, _, err := svc.CreateExam(ctx, exam.CreateExamRequest{
		BankID:        bankID,
		QuestionCount: 201,
	}, "test-key-excessive", "digest-excessive")
	if err == nil {
		t.Fatal("expected error when question_count exceeds maxQuestions, got nil")
	}
	if !errors.Is(err, exam.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got: %v", err)
	}
	if !strings.Contains(err.Error(), "question_count exceeds maximum allowed") {
		t.Errorf("expected error message to mention question_count exceeds maximum allowed, got: %v", err)
	}
}

func TestSettleExpiredExamsErrorPropagation(t *testing.T) {
	db, svc := setupTestDB(t, "core")
	ctx := context.Background()
	bankID := "00000000-0000-4000-8000-000000000801"

	// Create a timed exam
	timeLimitSec := 60
	createResp, _, err := svc.CreateExam(ctx, exam.CreateExamRequest{
		BankID:           bankID,
		QuestionCount:    2,
		TimeLimitSeconds: &timeLimitSec,
	}, "key-timed", "digest-timed")
	if err != nil {
		t.Fatalf("CreateExam failed: %v", err)
	}

	// Update session deadline to be in the past
	pastMicro := time.Now().Add(-1 * time.Hour).UnixMicro()
	_, err = db.Exec("UPDATE session SET deadline_at = ? WHERE id = ?", pastMicro, createResp.ExamID)
	if err != nil {
		t.Fatalf("update deadline failed: %v", err)
	}

	// Inject a trigger fault on assessment_result to simulate settlement failure
	_, err = db.Exec("CREATE TRIGGER test_fault_settlement BEFORE INSERT ON assessment_result BEGIN SELECT RAISE(ABORT, 'injected settlement failure'); END")
	if err != nil {
		t.Fatalf("create trigger failed: %v", err)
	}
	defer func() {
		_, _ = db.Exec("DROP TRIGGER IF EXISTS test_fault_settlement")
	}()

	_, err = svc.SettleExpiredExams(ctx)
	if err == nil {
		t.Fatal("expected SettleExpiredExams to propagate settlement error, got nil")
	}
	if !strings.Contains(err.Error(), "injected settlement failure") {
		t.Errorf("expected error to contain 'injected settlement failure', got: %v", err)
	}
}

