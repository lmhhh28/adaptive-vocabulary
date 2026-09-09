package generation

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"adaptive-vocabulary/internal/content"
	"adaptive-vocabulary/internal/core"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	f, err := os.CreateTemp("", "gen_test_*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	dbPath := f.Name()
	_ = f.Close()

	db, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)

	_, err = core.RunMigrations(db)
	if err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	// Apply core seed
	if err := content.ApplySeed(context.Background(), db, "core"); err != nil {
		t.Fatalf("apply seed: %v", err)
	}

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
	}
	return db, cleanup
}

func TestJobCreationAndClaim(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policy := DefaultGenerationConfig()
	svc := NewService(db, policy)
	ctx := context.Background()
	now := time.Now().UTC().UnixMicro()

	// 1. Get bank and senses from core seed
	var bankID string
	var bankRev int
	err := db.QueryRow("SELECT id, revision FROM bank WHERE name = '开发样例'").Scan(&bankID, &bankRev)
	if err != nil {
		t.Fatalf("query bank: %v", err)
	}

	rows, err := db.Query("SELECT s.id, sv.id FROM sense s JOIN sense_version sv ON sv.sense_id = s.id JOIN bank_sense bs ON bs.sense_id = s.id WHERE bs.bank_id = ? LIMIT 2", bankID)
	if err != nil {
		t.Fatalf("query senses: %v", err)
	}
	defer rows.Close()

	var senseRefs []SenseRef
	for rows.Next() {
		var ref SenseRef
		_ = rows.Scan(&ref.SenseID, &ref.SenseVersionID)
		senseRefs = append(senseRefs, ref)
	}

	if len(senseRefs) == 0 {
		t.Fatalf("expected senses in bank")
	}

	// 2. Create job
	createResp, _, err := svc.CreateJob(ctx, bankID, CreateJobRequest{
		SenseRefs:               senseRefs,
		GenerationConfigVersion: "v1",
		QualityPolicyVersion:    "v1",
		ExpectedRevision:        bankRev,
	}, "", "", now)
	if err != nil {
		t.Fatalf("create job failed: %v", err)
	}
	if createResp.JobID == "" || createResp.FixedInputDigest == "" {
		t.Fatalf("invalid create response: %+v", createResp)
	}

	// 3. Worker A claims item
	claimResp, err := svc.ClaimItems(ctx, ClaimRequest{
		WorkerID: "worker-A",
		Limit:    1,
	}, now)
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	if len(claimResp.Items) != 1 {
		t.Fatalf("expected 1 claimed item, got %d", len(claimResp.Items))
	}
	claimed := claimResp.Items[0]
	if claimed.FencingToken != 1 {
		t.Fatalf("expected fencing token 1, got %d", claimed.FencingToken)
	}

	// 4. Renew lease
	renewResp, err := svc.RenewLease(ctx, RenewRequest{
		WorkerID:     "worker-A",
		ItemID:       claimed.ItemID,
		FencingToken: claimed.FencingToken,
		InputDigest:  claimed.InputDigest,
	}, now+1000000)
	if err != nil {
		t.Fatalf("renew failed: %v", err)
	}
	if renewResp.LeaseUntil <= claimed.LeaseUntil {
		t.Fatalf("expected extended lease")
	}

	// 5. Simulate lease expiration and takeover by Worker B
	futureTime := renewResp.LeaseUntil + 5000000 // 5 seconds after lease expired
	claimResp2, err := svc.ClaimItems(ctx, ClaimRequest{
		WorkerID: "worker-B",
		Limit:    1,
	}, futureTime)
	if err != nil {
		t.Fatalf("worker-B claim failed: %v", err)
	}
	// It should take over the expired item
	var tookOver *ClaimedItem
	for _, it := range claimResp2.Items {
		if it.ItemID == claimed.ItemID {
			tookOver = &it
			break
		}
	}
	if tookOver == nil {
		t.Fatalf("worker-B did not take over expired item")
	}
	if tookOver.FencingToken != 2 {
		t.Fatalf("expected incremented fencing token 2, got %d", tookOver.FencingToken)
	}

	// 6. Worker A tries to renew with old fence -> returns ErrLeaseLost
	_, err = svc.RenewLease(ctx, RenewRequest{
		WorkerID:     "worker-A",
		ItemID:       claimed.ItemID,
		FencingToken: claimed.FencingToken,
		InputDigest:  claimed.InputDigest,
	}, futureTime)
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expected ErrLeaseLost for old fence, got: %v", err)
	}
}

func TestBudgetLedgerAndCalls(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policy := DefaultGenerationConfig()
	policy.MaxRequestsPerJob = 3
	policy.MaxTokensPerJob = 5000
	svc := NewService(db, policy)
	ctx := context.Background()
	now := time.Now().UTC().UnixMicro()

	var bankID string
	var bankRev int
	_ = db.QueryRow("SELECT id, revision FROM bank WHERE name = '开发样例'").Scan(&bankID, &bankRev)

	var senseID, senseVersionID string
	_ = db.QueryRow("SELECT s.id, sv.id FROM sense s JOIN sense_version sv ON sv.sense_id = s.id JOIN bank_sense bs ON bs.sense_id = s.id WHERE bs.bank_id = ? LIMIT 1", bankID).
		Scan(&senseID, &senseVersionID)

	createResp, _, err := svc.CreateJob(ctx, bankID, CreateJobRequest{
		SenseRefs: []SenseRef{
			{SenseID: senseID, SenseVersionID: senseVersionID},
		},
		GenerationConfigVersion: "v1",
		QualityPolicyVersion:    "v1",
		ExpectedRevision:        bankRev,
	}, "", "", now)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	claimResp, err := svc.ClaimItems(ctx, ClaimRequest{
		WorkerID: "worker-1",
		Limit:    1,
	}, now)
	if err != nil || len(claimResp.Items) == 0 {
		t.Fatalf("claim: %v", err)
	}
	item := claimResp.Items[0]

	// Reserve Call 1 (Generator, attempt 1)
	res1, _, err := svc.ReserveCall(ctx, ReserveRequest{
		WorkerID:           "worker-1",
		ItemID:             item.ItemID,
		FencingToken:       item.FencingToken,
		InputDigest:        item.InputDigest,
		LogicalCallKey:     LogicalCallKey{AttemptNumber: 1, Stage: "generator", CandidateIndex: 0},
		NetworkAttemptID:   core.NewUUID(),
		ProfileVersion:     "v1",
		InputSHA256:        "fakeinputsha",
		InputTokenEstimate: 500,
		MaxOutputTokens:    500,
	}, "", "", now)
	if err != nil {
		t.Fatalf("reserve 1 failed: %v", err)
	}
	if res1.ReservedTokens != 1000 || res1.AuthorizedRequests != 1 {
		t.Fatalf("unexpected res1: %+v", res1)
	}

	// Settle Call 1 with usage
	settle1, err := svc.SettleCall(ctx, res1.ReservationID, SettleRequest{
		WorkerID:     "worker-1",
		ItemID:       item.ItemID,
		FencingToken: item.FencingToken,
		InputDigest:  item.InputDigest,
		Outcome:      "response",
		Usage: &TokenUsage{
			InputTokens:  400,
			OutputTokens: 400,
		},
	}, now)
	if err != nil {
		t.Fatalf("settle 1 failed: %v", err)
	}
	if settle1.AccountedTokens != 800 {
		t.Fatalf("expected 800 accounted tokens, got %d", settle1.AccountedTokens)
	}

	// Settle again with exact same digest -> idempotent replay
	settle1Replay, err := svc.SettleCall(ctx, res1.ReservationID, SettleRequest{
		WorkerID:     "worker-1",
		ItemID:       item.ItemID,
		FencingToken: item.FencingToken,
		InputDigest:  item.InputDigest,
		Outcome:      "response",
		Usage: &TokenUsage{
			InputTokens:  400,
			OutputTokens: 400,
		},
	}, now)
	if err != nil {
		t.Fatalf("settle 1 replay failed: %v", err)
	}
	if settle1Replay.AccountedTokens != 800 {
		t.Fatalf("expected 800 on replay")
	}

	// Settle with differing digest -> conflict!
	_, err = svc.SettleCall(ctx, res1.ReservationID, SettleRequest{
		WorkerID:     "worker-1",
		ItemID:       item.ItemID,
		FencingToken: item.FencingToken,
		InputDigest:  item.InputDigest,
		Outcome:      "network_error",
	}, now)
	if !errors.Is(err, ErrCallSettlementConflict) {
		t.Fatalf("expected ErrCallSettlementConflict, got %v", err)
	}

	// Test 3 network attempts limit per logical call
	key := LogicalCallKey{AttemptNumber: 1, Stage: "evaluator", CandidateIndex: 0, EvaluationIndex: 1}
	// Attempt 1
	r1, _, err := svc.ReserveCall(ctx, ReserveRequest{
		WorkerID: "worker-1", ItemID: item.ItemID, FencingToken: item.FencingToken, InputDigest: item.InputDigest,
		LogicalCallKey: key, NetworkAttemptID: core.NewUUID(), ProfileVersion: "v1", InputSHA256: "s1", InputTokenEstimate: 100, MaxOutputTokens: 100,
	}, "", "", now)
	if err != nil {
		t.Fatalf("r1: %v", err)
	}
	_ = r1

	// Attempt 2
	r2, _, err := svc.ReserveCall(ctx, ReserveRequest{
		WorkerID: "worker-1", ItemID: item.ItemID, FencingToken: item.FencingToken, InputDigest: item.InputDigest,
		LogicalCallKey: key, NetworkAttemptID: core.NewUUID(), ProfileVersion: "v1", InputSHA256: "s1", InputTokenEstimate: 100, MaxOutputTokens: 100,
	}, "", "", now)
	if err != nil {
		t.Fatalf("r2: %v", err)
	}
	_ = r2

	// Now authorized requests reached max (1 + 1 + 1 = 3 = maxRequests)!
	// Next attempt should fail with BUDGET_EXHAUSTED
	_, _, err = svc.ReserveCall(ctx, ReserveRequest{
		WorkerID: "worker-1", ItemID: item.ItemID, FencingToken: item.FencingToken, InputDigest: item.InputDigest,
		LogicalCallKey: key, NetworkAttemptID: core.NewUUID(), ProfileVersion: "v1", InputSHA256: "s1", InputTokenEstimate: 100, MaxOutputTokens: 100,
	}, "", "", now)
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("expected ErrBudgetExhausted, got %v", err)
	}

	job, err := svc.GetJob(ctx, createResp.JobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.BudgetUsage.AuthorizedRequests != 3 {
		t.Fatalf("expected 3 authorized requests, got %d", job.BudgetUsage.AuthorizedRequests)
	}
}

func TestCandidatePublishing(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policy := DefaultGenerationConfig()
	svc := NewService(db, policy)
	ctx := context.Background()
	now := time.Now().UTC().UnixMicro()

	var bankID string
	var bankRev int
	_ = db.QueryRow("SELECT id, revision FROM bank WHERE name = '开发样例'").Scan(&bankID, &bankRev)

	var senseID, senseVersionID string
	_ = db.QueryRow("SELECT s.id, sv.id FROM sense s JOIN sense_version sv ON sv.sense_id = s.id JOIN bank_sense bs ON bs.sense_id = s.id WHERE bs.bank_id = ? LIMIT 1", bankID).
		Scan(&senseID, &senseVersionID)

	_, _, err := svc.CreateJob(ctx, bankID, CreateJobRequest{
		SenseRefs: []SenseRef{
			{SenseID: senseID, SenseVersionID: senseVersionID},
		},
		GenerationConfigVersion: "v1",
		QualityPolicyVersion:    "v1",
		ExpectedRevision:        bankRev,
	}, "", "", now)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	claimResp, err := svc.ClaimItems(ctx, ClaimRequest{
		WorkerID: "worker-pub",
		Limit:    1,
	}, now)
	if err != nil || len(claimResp.Items) == 0 {
		t.Fatalf("claim: %v", err)
	}
	item := claimResp.Items[0]

	// Generator reservation
	genRes, _, err := svc.ReserveCall(ctx, ReserveRequest{
		WorkerID: "worker-pub", ItemID: item.ItemID, FencingToken: item.FencingToken, InputDigest: item.InputDigest,
		LogicalCallKey: LogicalCallKey{AttemptNumber: 1, Stage: "generator", CandidateIndex: 0},
		NetworkAttemptID: core.NewUUID(), ProfileVersion: "v1", InputSHA256: "in1", InputTokenEstimate: 200, MaxOutputTokens: 200,
	}, "", "", now)
	if err != nil {
		t.Fatalf("gen reserve: %v", err)
	}

	// Evaluator 1 reservation
	evalRes1, _, err := svc.ReserveCall(ctx, ReserveRequest{
		WorkerID: "worker-pub", ItemID: item.ItemID, FencingToken: item.FencingToken, InputDigest: item.InputDigest,
		LogicalCallKey: LogicalCallKey{AttemptNumber: 1, Stage: "evaluator", CandidateIndex: 0, EvaluationIndex: 1},
		NetworkAttemptID: core.NewUUID(), ProfileVersion: "v1", InputSHA256: "in2", InputTokenEstimate: 200, MaxOutputTokens: 200,
	}, "", "", now)
	if err != nil {
		t.Fatalf("eval1 reserve: %v", err)
	}

	// Evaluator 2 reservation
	evalRes2, _, err := svc.ReserveCall(ctx, ReserveRequest{
		WorkerID: "worker-pub", ItemID: item.ItemID, FencingToken: item.FencingToken, InputDigest: item.InputDigest,
		LogicalCallKey: LogicalCallKey{AttemptNumber: 1, Stage: "evaluator", CandidateIndex: 0, EvaluationIndex: 2},
		NetworkAttemptID: core.NewUUID(), ProfileVersion: "v1", InputSHA256: "in3", InputTokenEstimate: 200, MaxOutputTokens: 200,
	}, "", "", now)
	if err != nil {
		t.Fatalf("eval2 reserve: %v", err)
	}

	candContent := CandidateContent{
		TaskFamily: "meaning_recognition",
		Stem:       "What is the meaning of the word?",
		Options: []CandidateOption{
			{Text: "正确词义", IsCorrect: true, Language: "zh", SenseID: senseID},
			{Text: "干扰项A", IsCorrect: false, Language: "zh"},
			{Text: "干扰项B", IsCorrect: false, Language: "zh"},
			{Text: "干扰项C", IsCorrect: false, Language: "zh"},
		},
		Explanation: "解析说明",
	}

	passRatings := map[string]string{
		"target_meaning":          "pass",
		"unique_answer":           "pass",
		"grammar":                 "pass",
		"naturalness":             "pass",
		"explanation_consistency": "pass",
		"distractor_validity":     "pass",
	}

	// Submit candidate with all pass
	subResp, _, err := svc.SubmitCandidate(ctx, SubmitCandidateRequest{
		WorkerID:          "worker-pub",
		ItemID:            item.ItemID,
		FencingToken:      item.FencingToken,
		InputDigest:       item.InputDigest,
		AttemptNumber:     1,
		CandidateIndex:    0,
		Content:           candContent,
		GenerationMetadata: GenerationMetadata{
			GeneratorReservationID:  genRes.ReservationID,
			EvaluatorReservationIDs: []string{evalRes1.ReservationID, evalRes2.ReservationID},
		},
		Checks: CandidateChecks{
			RuleChecks: map[string]any{"options_count": 4},
			SemanticEvaluations: []SemanticEvaluation{
				{ProfileVersion: "v1", Model: "eval-m1", Ratings: passRatings, Rationale: "good"},
				{ProfileVersion: "v1", Model: "eval-m2", Ratings: passRatings, Rationale: "good"},
			},
			Result: "pass",
		},
	}, "", "", now)
	if err != nil {
		t.Fatalf("submit candidate: %v", err)
	}

	if subResp.Disposition != "published" || subResp.PublishedVersionID == nil {
		t.Fatalf("expected published, got %+v", subResp)
	}

	// Verify question_version is published
	var qvStatus string
	err = db.QueryRow("SELECT status FROM question_version WHERE id = ?", *subResp.PublishedVersionID).Scan(&qvStatus)
	if err != nil || qvStatus != "published" {
		t.Fatalf("expected published question_version, got status %s, err %v", qvStatus, err)
	}
}
