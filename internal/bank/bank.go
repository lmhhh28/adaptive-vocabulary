package bank

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"adaptive-vocabulary/internal/core"
)

var (
	ErrBankNotFound     = errors.New("bank not found")
	ErrInvalidBankName  = errors.New("bank name must be between 1 and 120 characters")
	ErrRevisionConflict = errors.New("revision conflict")
	ErrSessionActive    = errors.New("active session exists")
	ErrUnderMaintenance = errors.New("system under maintenance")
	ErrInvalidMemberIDs = errors.New("sense_ids must contain 1 to 1000 items")
)

type BankSummary struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Note        string         `json:"note"`
	Archived    bool           `json:"archived"`
	Revision    int64          `json:"revision"`
	MemberCount int            `json:"member_count"`
	TaskCounts  map[string]int `json:"task_counts"`
	CreatedAt   int64          `json:"created_at"`
	UpdatedAt   int64          `json:"updated_at"`
}

type BankMember struct {
	SenseID       string `json:"sense_id"`
	CanonicalForm string `json:"canonical_form"`
	PartOfSpeech  string `json:"part_of_speech"`
	Meaning       string `json:"meaning"`
	SourceID      string `json:"source_id"`
	AddedAt       int64  `json:"added_at"`
}

type BankDetail struct {
	BankSummary
	Members []BankMember `json:"members"`
}

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

func (s *Service) CreateBank(ctx context.Context, name, note string) (*BankSummary, error) {
	trimmed := strings.TrimSpace(name)
	if len(trimmed) < 1 || len(trimmed) > 120 {
		return nil, ErrInvalidBankName
	}

	id := core.NewUUID()
	now := time.Now().UTC().UnixMicro()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO bank (id, name, note, archived, revision, created_at, updated_at)
		VALUES (?, ?, ?, 0, 0, ?, ?)
	`, id, trimmed, strings.TrimSpace(note), now, now)
	if err != nil {
		return nil, fmt.Errorf("insert bank: %w", err)
	}

	return &BankSummary{
		ID:          id,
		Name:        trimmed,
		Note:        strings.TrimSpace(note),
		Archived:    false,
		Revision:    0,
		MemberCount: 0,
		TaskCounts:  map[string]int{"meaning_recognition": 0, "reverse_selection": 0, "context_selection": 0},
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func (s *Service) ListBanks(ctx context.Context) ([]BankSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, note, archived, revision, created_at, updated_at
		FROM bank
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list banks: %w", err)
	}
	defer rows.Close()

	var banks []BankSummary
	for rows.Next() {
		var b BankSummary
		var arch int
		if err := rows.Scan(&b.ID, &b.Name, &b.Note, &arch, &b.Revision, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan bank: %w", err)
		}
		b.Archived = (arch == 1)
		b.TaskCounts = map[string]int{"meaning_recognition": 0, "reverse_selection": 0, "context_selection": 0}
		banks = append(banks, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range banks {
		// Member count
		var count int
		_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM bank_sense WHERE bank_id = ?", banks[i].ID).Scan(&count)
		banks[i].MemberCount = count

		// Task counts
		tRows, err := s.db.QueryContext(ctx, `
			SELECT qv.task_family, COUNT(DISTINCT qv.id)
			FROM bank_sense bs
			JOIN question_version qv ON bs.sense_id = qv.sense_id
			WHERE bs.bank_id = ? AND qv.status = 'published'
			GROUP BY qv.task_family
		`, banks[i].ID)
		if err == nil {
			for tRows.Next() {
				var tf string
				var c int
				if err := tRows.Scan(&tf, &c); err == nil {
					banks[i].TaskCounts[tf] = c
				}
			}
			tRows.Close()
		}
	}

	return banks, nil
}

func (s *Service) GetBank(ctx context.Context, bankID string) (*BankDetail, error) {
	var b BankDetail
	var arch int
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, note, archived, revision, created_at, updated_at
		FROM bank
		WHERE id = ?
	`, bankID).Scan(&b.ID, &b.Name, &b.Note, &arch, &b.Revision, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrBankNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query bank: %w", err)
	}
	b.Archived = (arch == 1)
	b.TaskCounts = map[string]int{"meaning_recognition": 0, "reverse_selection": 0, "context_selection": 0}

	// Member count & Task counts
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM bank_sense WHERE bank_id = ?", bankID).Scan(&b.MemberCount)

	tRows, err := s.db.QueryContext(ctx, `
		SELECT qv.task_family, COUNT(DISTINCT qv.id)
		FROM bank_sense bs
		JOIN question_version qv ON bs.sense_id = qv.sense_id
		WHERE bs.bank_id = ? AND qv.status = 'published'
		GROUP BY qv.task_family
	`, bankID)
	if err == nil {
		for tRows.Next() {
			var tf string
			var c int
			if err := tRows.Scan(&tf, &c); err == nil {
				b.TaskCounts[tf] = c
			}
		}
		tRows.Close()
	}

	// Members
	mRows, err := s.db.QueryContext(ctx, `
		SELECT bs.sense_id, l.canonical_form, s.part_of_speech, sv.meaning, s.source_id, bs.added_at
		FROM bank_sense bs
		JOIN sense s ON bs.sense_id = s.id
		JOIN lexeme l ON s.lexeme_id = l.id
		JOIN sense_version sv ON sv.id = (
			SELECT sv2.id FROM sense_version sv2
			WHERE sv2.sense_id = s.id
			ORDER BY sv2.version_number DESC, sv2.created_at DESC
			LIMIT 1
		)
		WHERE bs.bank_id = ?
		ORDER BY bs.added_at ASC, l.canonical_form ASC
	`, bankID)
	if err != nil {
		return nil, fmt.Errorf("query bank members: %w", err)
	}
	defer mRows.Close()

	b.Members = make([]BankMember, 0)
	for mRows.Next() {
		var m BankMember
		if err := mRows.Scan(&m.SenseID, &m.CanonicalForm, &m.PartOfSpeech, &m.Meaning, &m.SourceID, &m.AddedAt); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		b.Members = append(b.Members, m)
	}

	return &b, nil
}

func (s *Service) RemoveMembersTx(tc *core.TxnContext, bankID string, senseIDs []string, expectedRevision int64) (removedCount int, newRev int64, currentRev int64, err error) {
	if len(senseIDs) < 1 || len(senseIDs) > 1000 {
		return 0, 0, 0, ErrInvalidMemberIDs
	}

	// 1. Check active session
	active, err := core.GetActiveSession(tc)
	if err != nil {
		return 0, 0, 0, err
	}
	if active != nil {
		return 0, 0, 0, ErrSessionActive
	}

	// 2. Check maintenance
	if err := core.CheckMaintenance(tc); err != nil {
		return 0, 0, 0, ErrUnderMaintenance
	}

	// 3. Check bank and revision
	var rev int64
	var arch int
	err = tc.QueryRow("SELECT revision, archived FROM bank WHERE id = ?", bankID).Scan(&rev, &arch)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, 0, ErrBankNotFound
	}
	if err != nil {
		return 0, 0, 0, fmt.Errorf("query bank revision: %w", err)
	}

	currentRev = rev
	if rev != expectedRevision {
		return 0, 0, currentRev, ErrRevisionConflict
	}

	// 4. Remove members
	now := time.Now().UTC().UnixMicro()
	placeholders := make([]string, len(senseIDs))
	args := make([]any, 0, len(senseIDs)+1)
	args = append(args, bankID)
	for i, sid := range senseIDs {
		placeholders[i] = "?"
		args = append(args, sid)
	}

	delQuery := fmt.Sprintf("DELETE FROM bank_sense WHERE bank_id = ? AND sense_id IN (%s)", strings.Join(placeholders, ","))
	res, err := tc.Exec(delQuery, args...)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("delete bank senses: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return 0, 0, 0, err
	}
	removedCount = int(rowsAffected)

	// 5. Bump revision
	newRev = rev + 1
	_, err = tc.Exec("UPDATE bank SET revision = ?, updated_at = ? WHERE id = ?", newRev, now, bankID)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("bump bank revision: %w", err)
	}

	return removedCount, newRev, currentRev, nil
}

func (s *Service) RemoveMembers(ctx context.Context, bankID string, senseIDs []string, expectedRevision int64) (removedCount int, newRev int64, currentRev int64, err error) {
	err = core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		var txErr error
		removedCount, newRev, currentRev, txErr = s.RemoveMembersTx(tc, bankID, senseIDs, expectedRevision)
		return txErr
	})

	return removedCount, newRev, currentRev, err
}

func (s *Service) ArchiveBankTx(tc *core.TxnContext, bankID string, expectedRevision int64) (newRev int64, currentRev int64, err error) {
	// 1. Check active session
	active, err := core.GetActiveSession(tc)
	if err != nil {
		return 0, 0, err
	}
	if active != nil {
		return 0, 0, ErrSessionActive
	}

	// 2. Check maintenance
	if err := core.CheckMaintenance(tc); err != nil {
		return 0, 0, ErrUnderMaintenance
	}

	// 3. Check bank and revision
	var rev int64
	var arch int
	err = tc.QueryRow("SELECT revision, archived FROM bank WHERE id = ?", bankID).Scan(&rev, &arch)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, ErrBankNotFound
	}
	if err != nil {
		return 0, 0, fmt.Errorf("query bank revision: %w", err)
	}

	currentRev = rev
	if rev != expectedRevision {
		return 0, currentRev, ErrRevisionConflict
	}

	now := time.Now().UTC().UnixMicro()
	newRev = rev + 1
	_, err = tc.Exec("UPDATE bank SET archived = 1, revision = ?, updated_at = ? WHERE id = ?", newRev, now, bankID)
	if err != nil {
		return 0, 0, fmt.Errorf("archive bank: %w", err)
	}

	return newRev, currentRev, nil
}

func (s *Service) ArchiveBank(ctx context.Context, bankID string, expectedRevision int64) (newRev int64, currentRev int64, err error) {
	err = core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		var txErr error
		newRev, currentRev, txErr = s.ArchiveBankTx(tc, bankID, expectedRevision)
		return txErr
	})

	return newRev, currentRev, err
}
