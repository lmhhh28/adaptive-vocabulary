package importer

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"adaptive-vocabulary/internal/bank"
	"adaptive-vocabulary/internal/content"
	"adaptive-vocabulary/internal/core"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}

	// Enable WAL & pragmas
	_, _ = db.Exec("PRAGMA foreign_keys = ON")

	_, err = core.RunMigrations(db)
	if err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	err = content.ApplySeed(context.Background(), db, "core")
	if err != nil {
		t.Fatalf("apply seed core: %v", err)
	}

	return db
}

func TestParseTXT(t *testing.T) {
	txt := "\n  river  \n\nforest\n  mountain\n"
	entries, err := ParseRawInput([]byte(txt), "test.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].NormalizedWord != "river" || entries[1].NormalizedWord != "forest" || entries[2].NormalizedWord != "mountain" {
		t.Fatalf("unexpected normalized words: %+v", entries)
	}
}

func TestParseCSV(t *testing.T) {
	csv := "word,sense_id\nriver,00000000-0000-4000-8000-000000000101\ndesert,\n"
	entries, err := ParseRawInput([]byte(csv), "test.csv")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].NormalizedWord != "river" || entries[0].SpecifiedSenseID == nil || *entries[0].SpecifiedSenseID != "00000000-0000-4000-8000-000000000101" {
		t.Fatalf("unexpected entry 0: %+v", entries[0])
	}
	if entries[1].NormalizedWord != "desert" || entries[1].SpecifiedSenseID != nil {
		t.Fatalf("unexpected entry 1: %+v", entries[1])
	}
}

func TestBankVocabularyImportAndResolution(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	bankSvc := bank.NewService(db)
	newBank, err := bankSvc.CreateBank(ctx, "Import Test Bank", "Testing M05")
	if err != nil {
		t.Fatalf("create bank: %v", err)
	}

	importerSvc := NewService(db)

	// In core seed, "river", "desert", "stream" exist in lexeme and sense.
	// "unknownxyz" does not exist.
	wordsInput := []string{
		"river",
		"desert",
		"unknownxyz",
		"   ", // empty, should be filtered or ignored
	}
	parsed, err := ParseWordSlice(wordsInput)
	if err != nil {
		t.Fatalf("parse word slice: %v", err)
	}

	job, err := importerSvc.ImportBankVocabulary(ctx, newBank.ID, parsed, "v1", newBank.Revision, "idemp-key-1", "digest-1")
	if err != nil {
		t.Fatalf("import bank vocabulary: %v", err)
	}

	if job.TotalCount != 3 {
		t.Fatalf("expected total 3, got %d", job.TotalCount)
	}
	if job.NotFoundCount != 1 {
		t.Fatalf("expected 1 not_found, got %d", job.NotFoundCount)
	}

	// Check items list
	itemsResp, err := importerSvc.GetImportItems(ctx, job.ID, "", 50, 0)
	if err != nil {
		t.Fatalf("get import items: %v", err)
	}
	if len(itemsResp.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(itemsResp.Items))
	}

	// Idempotency re-call
	job2, err := importerSvc.ImportBankVocabulary(ctx, newBank.ID, parsed, "v1", newBank.Revision, "idemp-key-1", "digest-1")
	if err != nil {
		t.Fatalf("idempotent replay failed: %v", err)
	}
	if job2.ID != job.ID {
		t.Fatalf("expected same job ID on replay")
	}
}

func TestResolveItem(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	bankSvc := bank.NewService(db)
	newBank, err := bankSvc.CreateBank(ctx, "Resolve Bank", "")
	if err != nil {
		t.Fatalf("create bank: %v", err)
	}

	importerSvc := NewService(db)

	// In core seed, "bank" has 2 senses (financial bank and river bank)
	parsed, err := ParseWordSlice([]string{"bank"})
	if err != nil {
		t.Fatalf("parse word slice: %v", err)
	}
	job, err := importerSvc.ImportBankVocabulary(ctx, newBank.ID, parsed, "v1", -1, "", "")
	if err != nil {
		t.Fatalf("import vocabulary: %v", err)
	}

	if job.AmbiguousCount != 1 {
		t.Fatalf("expected 1 ambiguous, got %d", job.AmbiguousCount)
	}

	itemsResp, err := importerSvc.GetImportItems(ctx, job.ID, "ambiguous", 10, 0)
	if err != nil || len(itemsResp.Items) != 1 {
		t.Fatalf("expected 1 ambiguous item, got %d, err: %v", len(itemsResp.Items), err)
	}
	item := itemsResp.Items[0]
	if len(item.Candidates) < 2 {
		t.Fatalf("expected at least 2 candidates, got %d", len(item.Candidates))
	}

	s1 := item.Candidates[0].SenseID

	// CAS conflict check
	_, err = importerSvc.ResolveImportItem(ctx, item.ID, ResolveItemRequest{
		Action:           "adopt",
		SenseRefs:        []string{s1},
		Reason:           "Adopt financial bank",
		ExpectedRevision: 999, // wrong revision
	}, "", "")
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("expected ErrRevisionConflict, got %v", err)
	}

	// Correct resolve: adopt s1
	updated, err := importerSvc.ResolveImportItem(ctx, item.ID, ResolveItemRequest{
		Action:           "adopt",
		SenseRefs:        []string{s1},
		Reason:           "Adopt financial bank",
		ExpectedRevision: item.Revision,
	}, "resolve-key-1", "digest-res-1")
	if err != nil {
		t.Fatalf("resolve adopt error: %v", err)
	}
	if updated.Status != "resolved" {
		t.Fatalf("expected resolved, got %s", updated.Status)
	}

	// Check bank members
	bankDetail, err := bankSvc.GetBank(ctx, newBank.ID)
	if err != nil {
		t.Fatalf("get bank detail: %v", err)
	}
	if bankDetail.MemberCount != 1 {
		t.Fatalf("expected 1 member in bank, got %d", bankDetail.MemberCount)
	}
}

func TestResolveItemExclude(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	bankSvc := bank.NewService(db)
	newBank, err := bankSvc.CreateBank(ctx, "Exclude Bank", "")
	if err != nil {
		t.Fatalf("create bank: %v", err)
	}

	importerSvc := NewService(db)
	parsed, err := ParseWordSlice([]string{"bank"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	job, err := importerSvc.ImportBankVocabulary(ctx, newBank.ID, parsed, "v1", -1, "", "")
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	itemsResp, err := importerSvc.GetImportItems(ctx, job.ID, "ambiguous", 10, 0)
	if err != nil || len(itemsResp.Items) != 1 {
		t.Fatalf("expected 1 ambiguous item, got %d", len(itemsResp.Items))
	}
	item := itemsResp.Items[0]

	// Exclude
	updated, err := importerSvc.ResolveImportItem(ctx, item.ID, ResolveItemRequest{
		Action:           "exclude",
		Reason:           "Not relevant for current study",
		ExpectedRevision: item.Revision,
	}, "", "")
	if err != nil {
		t.Fatalf("resolve exclude error: %v", err)
	}
	if updated.Status != "excluded" {
		t.Fatalf("expected excluded status, got %s", updated.Status)
	}

	// Verify bank still has 0 members
	bDetail, _ := bankSvc.GetBank(ctx, newBank.ID)
	if bDetail.MemberCount != 0 {
		t.Fatalf("expected 0 members, got %d", bDetail.MemberCount)
	}
}

func TestSourceImport(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	importerSvc := NewService(db)

	pos := "n."
	tag := "cet6"
	req := SourceImportRequest{
		SourceVersion: "1.0.0",
		Entries: []SourceEntryInput{
			{
				ExternalEntryID: "entry-101",
				RawWord:         "ocean",
				RawTranslation: "海洋",
				POS:             &pos,
				Tags:            &tag,
			},
			{
				ExternalEntryID: "entry-102",
				RawWord:         "stream",
				RawTranslation: "小溪",
				POS:             &pos,
			},
		},
		ExpectedRevision: 0,
	}

	job, err := importerSvc.ImportSourceEntries(ctx, "project-authored-fixture-v1", req, "src-idemp-1", "src-digest-1")
	if err != nil {
		t.Fatalf("import source entries: %v", err)
	}
	if job.TotalCount != 2 {
		t.Fatalf("expected 2 total entries, got %d", job.TotalCount)
	}

	// Verify source_entry table has ocean
	var c int
	_ = db.QueryRow("SELECT COUNT(*) FROM source_entry WHERE external_entry_id = 'entry-101'").Scan(&c)
	if c != 1 {
		t.Fatalf("expected source_entry entry-101 to exist")
	}

	// Verify sense was created for ocean
	var senseCount int
	_ = db.QueryRow(`
		SELECT COUNT(*) FROM sense s
		JOIN lexeme l ON l.id = s.lexeme_id
		WHERE l.canonical_form = 'ocean'
	`).Scan(&senseCount)
	if senseCount != 1 {
		t.Fatalf("expected 1 sense for ocean, got %d", senseCount)
	}
}

func TestParseConstraints(t *testing.T) {
	// Over 2 MiB file
	largeData := []byte(strings.Repeat("a\n", 2*1024*1024+10))
	_, err := ParseRawInput(largeData, "large.txt")
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("expected ErrFileTooLarge, got %v", err)
	}

	// Over 5000 rows
	rowsData := []byte(strings.Repeat("word\n", 5001))
	_, err = ParseRawInput(rowsData, "rows.txt")
	if !errors.Is(err, ErrTooManyRows) {
		t.Fatalf("expected ErrTooManyRows, got %v", err)
	}
}

func TestBankSenseInsertErrorPropagated(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	bankSvc := bank.NewService(db)
	newBank, err := bankSvc.CreateBank(ctx, "Trigger Bank", "")
	if err != nil {
		t.Fatalf("create bank: %v", err)
	}

	importerSvc := NewService(db)
	parsed, err := ParseWordSlice([]string{"river"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Add abort trigger on bank_sense to simulate insert failure
	_, err = db.Exec(`CREATE TRIGGER fail_bank_sense BEFORE INSERT ON bank_sense BEGIN SELECT RAISE(ABORT, 'injected bank_sense insert error'); END;`)
	if err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err = importerSvc.ImportBankVocabulary(ctx, newBank.ID, parsed, "v1", -1, "", "")
	if err == nil {
		t.Fatalf("expected error from failed bank_sense insert, got nil")
	}

	// Ensure no job was recorded or committed
	var jobCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM import_job WHERE bank_id = ?", newBank.ID).Scan(&jobCount)
	if jobCount != 0 {
		t.Fatalf("expected 0 jobs after rollback, got %d", jobCount)
	}
}

func TestResolveItemRejectsFinalizedStatus(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	bankSvc := bank.NewService(db)
	newBank, err := bankSvc.CreateBank(ctx, "Conflict Bank", "")
	if err != nil {
		t.Fatalf("create bank: %v", err)
	}

	importerSvc := NewService(db)
	parsed, err := ParseWordSlice([]string{"bank"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	job, err := importerSvc.ImportBankVocabulary(ctx, newBank.ID, parsed, "v1", -1, "", "")
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	itemsResp, err := importerSvc.GetImportItems(ctx, job.ID, "ambiguous", 10, 0)
	if err != nil || len(itemsResp.Items) != 1 {
		t.Fatalf("expected 1 ambiguous item, got %d, err: %v", len(itemsResp.Items), err)
	}
	item := itemsResp.Items[0]
	s1 := item.Candidates[0].SenseID

	// Adopt s1 -> becomes resolved
	updated, err := importerSvc.ResolveImportItem(ctx, item.ID, ResolveItemRequest{
		Action:           "adopt",
		SenseRefs:        []string{s1},
		Reason:           "Adopt first sense",
		ExpectedRevision: item.Revision,
	}, "", "")
	if err != nil {
		t.Fatalf("resolve adopt error: %v", err)
	}
	if updated.Status != "resolved" {
		t.Fatalf("expected resolved, got %s", updated.Status)
	}

	// Attempt to exclude already resolved item -> must fail with ErrInvalidItemStatus
	_, err = importerSvc.ResolveImportItem(ctx, item.ID, ResolveItemRequest{
		Action:           "exclude",
		Reason:           "Exclude after resolve",
		ExpectedRevision: updated.Revision,
	}, "", "")
	if !errors.Is(err, ErrInvalidItemStatus) {
		t.Fatalf("expected ErrInvalidItemStatus, got %v", err)
	}
}

func TestMultiVersionSenseDoesNotDuplicateCandidates(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	// Find sense ID for river
	var riverSenseID string
	err := db.QueryRow(`
		SELECT s.id FROM sense s
		JOIN lexeme l ON l.id = s.lexeme_id
		WHERE l.canonical_form = 'river'
	`).Scan(&riverSenseID)
	if err != nil {
		t.Fatalf("query river sense: %v", err)
	}

	// Insert a version 2 for river sense
	now := int64(1700000000000)
	_, err = db.Exec(`
		INSERT INTO sense_version (id, sense_id, version_number, meaning, created_at)
		VALUES ('ver-river-2', ?, 2, 'A large natural stream of water (v2)', ?)
	`, riverSenseID, now)
	if err != nil {
		t.Fatalf("insert sense_version v2: %v", err)
	}

	bankSvc := bank.NewService(db)
	newBank, err := bankSvc.CreateBank(ctx, "Multi-version Bank", "")
	if err != nil {
		t.Fatalf("create bank: %v", err)
	}

	importerSvc := NewService(db)
	parsed, err := ParseWordSlice([]string{"river"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	job, err := importerSvc.ImportBankVocabulary(ctx, newBank.ID, parsed, "v1", -1, "", "")
	if err != nil {
		t.Fatalf("import river: %v", err)
	}

	// Because of single sense with multiple versions, it should NOT be ambiguous
	if job.ResolvedCount != 1 || job.AmbiguousCount != 0 {
		t.Fatalf("expected 1 resolved and 0 ambiguous, got %d resolved, %d ambiguous", job.ResolvedCount, job.AmbiguousCount)
	}
}

func TestImportAndResolveRejectActiveSession(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	bankSvc := bank.NewService(db)
	newBank, err := bankSvc.CreateBank(ctx, "Active Session Bank", "")
	if err != nil {
		t.Fatalf("create bank: %v", err)
	}

	importerSvc := NewService(db)
	parsed, err := ParseWordSlice([]string{"river", "unknownword"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Create an import job first
	job, err := importerSvc.ImportBankVocabulary(ctx, newBank.ID, parsed, "v1", -1, "", "")
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	// Fetch the ambiguous/unresolved item
	itemsResp, err := importerSvc.GetImportItems(ctx, job.ID, "", 50, 0)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	var unresolvedID string
	var unresolvedRev int64
	for _, it := range itemsResp.Items {
		if it.Status != "resolved" {
			unresolvedID = it.ID
			unresolvedRev = it.Revision
			break
		}
	}
	if unresolvedID == "" {
		t.Fatalf("expected at least one unresolved item")
	}

	// Open a session on this bank
	now := int64(1700000000000)
	_, err = db.Exec(`
		INSERT INTO session (id, bank_id, kind, status, created_at, updated_at)
		VALUES ('test-open-session', ?, 'practice', 'open', ?, ?)
	`, newBank.ID, now, now)
	if err != nil {
		t.Fatalf("insert open session: %v", err)
	}

	// 1. Attempting new ImportBankVocabulary should fail with ErrSessionActive
	_, err = importerSvc.ImportBankVocabulary(ctx, newBank.ID, parsed, "v1", -1, "", "")
	if !errors.Is(err, ErrSessionActive) {
		t.Fatalf("expected ErrSessionActive for ImportBankVocabulary, got: %v", err)
	}

	// 2. Attempting to resolve item with adopt should fail with ErrSessionActive
	var senseID string
	_ = db.QueryRow("SELECT id FROM sense LIMIT 1").Scan(&senseID)
	_, err = importerSvc.ResolveImportItem(ctx, unresolvedID, ResolveItemRequest{
		Action:           "adopt",
		SenseRefs:        []string{senseID},
		ExpectedRevision: unresolvedRev,
	}, "", "")
	if !errors.Is(err, ErrSessionActive) {
		t.Fatalf("expected ErrSessionActive for ResolveImportItem, got: %v", err)
	}
}

func TestIdempotencyErrorCausesRollback(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	bankSvc := bank.NewService(db)
	newBank, err := bankSvc.CreateBank(ctx, "Idemp Bank", "")
	if err != nil {
		t.Fatalf("create bank: %v", err)
	}

	importerSvc := NewService(db)
	parsed, err := ParseWordSlice([]string{"river"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Injected trigger fault on idempotency_record
	_, err = db.Exec("CREATE TRIGGER fault_idempotency BEFORE INSERT ON idempotency_record BEGIN SELECT RAISE(ABORT, 'injected idemp failure'); END")
	if err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, err = importerSvc.ImportBankVocabulary(ctx, newBank.ID, parsed, "v1", -1, "test-idemp-key", "test-digest")
	if err == nil {
		t.Fatal("expected error from ImportBankVocabulary due to idempotency failure, got nil")
	}
	if !strings.Contains(err.Error(), "injected idemp failure") {
		t.Errorf("expected error to contain injected failure, got: %v", err)
	}

	// Verify nothing was committed: import_job should have 0 rows
	var count int
	_ = db.QueryRow("SELECT count(*) FROM import_job WHERE target_id = ?", newBank.ID).Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 import_job records after rollback, got %d", count)
	}
}


