package bank

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestGetBankMultiVersionSenseNoDuplicates(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	if err := content.ApplySeed(ctx, db, "core"); err != nil {
		t.Fatalf("apply seed core: %v", err)
	}

	svc := NewService(db)
	newBank, err := svc.CreateBank(ctx, "Single Member Bank", "")
	if err != nil {
		t.Fatalf("create bank: %v", err)
	}

	// Pick a sense from seed
	var senseID string
	err = db.QueryRow("SELECT id FROM sense LIMIT 1").Scan(&senseID)
	if err != nil {
		t.Fatalf("query sense: %v", err)
	}

	now := 1000000
	_, err = db.Exec("INSERT INTO bank_sense (bank_id, sense_id, added_at) VALUES (?, ?, ?)", newBank.ID, senseID, now)
	if err != nil {
		t.Fatalf("insert bank_sense: %v", err)
	}

	// Add a version 2 to this sense
	_, err = db.Exec(`
		INSERT INTO sense_version (id, sense_id, version_number, meaning, created_at)
		VALUES ('ver-test-2', ?, 2, 'New meaning v2', ?)
	`, senseID, now+1000)
	if err != nil {
		t.Fatalf("insert sense_version v2: %v", err)
	}

	detail, err := svc.GetBank(ctx, newBank.ID)
	if err != nil {
		t.Fatalf("GetBank: %v", err)
	}
	if detail.MemberCount != 1 {
		t.Fatalf("expected member_count 1, got %d", detail.MemberCount)
	}
	if len(detail.Members) != 1 {
		t.Fatalf("expected 1 member row, got %d", len(detail.Members))
	}
	if detail.Members[0].Meaning != "New meaning v2" {
		t.Fatalf("expected latest meaning 'New meaning v2', got %q", detail.Members[0].Meaning)
	}
}

func TestContentVersionDiscovery(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	if err := content.ApplySeed(ctx, db, "core"); err != nil {
		t.Fatalf("apply seed core: %v", err)
	}

	svc := NewService(db)

	// Discover sense versions without filter
	senses, err := svc.ListSenseVersions(ctx, "", "", 10)
	if err != nil {
		t.Fatalf("ListSenseVersions failed: %v", err)
	}
	if len(senses) == 0 {
		t.Fatalf("expected non-empty sense versions from core seed")
	}
	for _, s := range senses {
		if s.SenseID == "" || s.SenseVersionID == "" || s.CanonicalForm == "" || s.Definition == "" || s.Status != "published" {
			t.Errorf("sense version item missing required fields: %+v", s)
		}
	}

	// Discover question versions
	questions, err := svc.ListQuestionVersions(ctx, "", "", 10)
	if err != nil {
		t.Fatalf("ListQuestionVersions failed: %v", err)
	}
	if len(questions) == 0 {
		t.Fatalf("expected non-empty question versions from core seed")
	}
	for _, q := range questions {
		if q.QuestionVersionID == "" || q.SenseID == "" || q.FormID == "" || q.TaskFamily == "" || q.Status == "" {
			t.Errorf("question version item missing required fields: %+v", q)
		}
	}
}

func TestContentDiscoveryHTTP(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	if err := content.ApplySeed(ctx, db, "core"); err != nil {
		t.Fatalf("apply seed core: %v", err)
	}

	handler := NewHandler(db)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	// 1. Successful GET /api/v1/content/sense-versions?limit=2
	req := httptest.NewRequest(http.MethodGet, "/api/v1/content/sense-versions?limit=2", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var senseResp struct {
		Items      []SenseVersionItem `json:"items"`
		NextCursor *string            `json:"next_cursor"`
		HasMore    bool               `json:"has_more"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &senseResp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(senseResp.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(senseResp.Items))
	}
	if !senseResp.HasMore || senseResp.NextCursor == nil {
		t.Fatalf("expected has_more true with next_cursor")
	}

	// Question-version discovery must expose the revision required by the
	// retire/evidence-disposition write contracts.
	reqQuestions := httptest.NewRequest(http.MethodGet, "/api/v1/content/question-versions?limit=1", nil)
	recQuestions := httptest.NewRecorder()
	mux.ServeHTTP(recQuestions, reqQuestions)
	if recQuestions.Code != http.StatusOK {
		t.Fatalf("expected 200 for question versions, got %d: %s", recQuestions.Code, recQuestions.Body.String())
	}
	var questionResp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(recQuestions.Body.Bytes(), &questionResp); err != nil {
		t.Fatalf("unmarshal question versions: %v", err)
	}
	if len(questionResp.Items) != 1 {
		t.Fatalf("expected one question version, got %d", len(questionResp.Items))
	}
	if _, ok := questionResp.Items[0]["revision"]; !ok {
		t.Fatalf("question version response omitted revision: %s", recQuestions.Body.String())
	}

	// Fetch next page with cursor
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/content/sense-versions?limit=2&cursor="+*senseResp.NextCursor, nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 on page 2, got %d", rec2.Code)
	}

	// 2. Invalid cursor returns 400
	reqBadCursor := httptest.NewRequest(http.MethodGet, "/api/v1/content/sense-versions?cursor=!!not-base64!!", nil)
	recBadCursor := httptest.NewRecorder()
	mux.ServeHTTP(recBadCursor, reqBadCursor)
	if recBadCursor.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid cursor, got %d", recBadCursor.Code)
	}

	// 3. Invalid limit returns 400
	reqBadLimit := httptest.NewRequest(http.MethodGet, "/api/v1/content/sense-versions?limit=999", nil)
	recBadLimit := httptest.NewRecorder()
	mux.ServeHTTP(recBadLimit, reqBadLimit)
	if recBadLimit.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on limit 999, got %d", recBadLimit.Code)
	}

	// 4. Active session protection returns 409
	now := core.NowMicro()
	_, err := db.Exec(`
		INSERT INTO session (id, bank_id, kind, status, created_at, updated_at)
		VALUES ('active-sess-test', '00000000-0000-4000-8000-000000000801', 'practice', 'open', ?, ?)
	`, now, now)
	if err != nil {
		t.Fatalf("insert active session: %v", err)
	}

	reqActive := httptest.NewRequest(http.MethodGet, "/api/v1/content/sense-versions", nil)
	recActive := httptest.NewRecorder()
	mux.ServeHTTP(recActive, reqActive)
	if recActive.Code != http.StatusConflict {
		t.Fatalf("expected 409 during active session, got %d: %s", recActive.Code, recActive.Body.String())
	}

	reqActiveQ := httptest.NewRequest(http.MethodGet, "/api/v1/content/question-versions", nil)
	recActiveQ := httptest.NewRecorder()
	mux.ServeHTTP(recActiveQ, reqActiveQ)
	if recActiveQ.Code != http.StatusConflict {
		t.Fatalf("expected 409 during active session for questions, got %d: %s", recActiveQ.Code, recActiveQ.Body.String())
	}

	// Clear active session
	_, _ = db.Exec("UPDATE session SET status = 'ended' WHERE id = 'active-sess-test'")

	// 5. Maintenance mode blocks write
	_, err = db.Exec("UPDATE projection_control SET mode = 'rebuilding'")
	if err != nil {
		t.Fatalf("set maintenance mode: %v", err)
	}

	createBankBody := `{"name": "Maint Bank", "note": "should fail"}`
	reqMaint := httptest.NewRequest(http.MethodPost, "/api/v1/banks", strings.NewReader(createBankBody))
	reqMaint.Header.Set("Idempotency-Key", "key-maint-test")
	reqMaint.Header.Set("Content-Type", "application/json")
	recMaint := httptest.NewRecorder()
	mux.ServeHTTP(recMaint, reqMaint)
	if recMaint.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 during maintenance, got %d: %s", recMaint.Code, recMaint.Body.String())
	}
}
