package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"adaptive-vocabulary/internal/core"
)

var (
	ErrInvalidOptionCount  = errors.New("question must have exactly 4 options")
	ErrDuplicateOption     = errors.New("options must have distinct texts and distinct option identities")
	ErrInvalidAnswerKey    = errors.New("exactly one option must match correct_option_id")
	ErrInvalidTaskFamily   = errors.New("invalid task_family")
	ErrTargetSenseNotFound = errors.New("target sense not found")
	ErrOptionSenseNotFound = errors.New("option referenced sense not found")
	ErrSourceNotFound      = errors.New("referenced source not found")
)

type QuestionDraft struct {
	SenseID         string           `json:"sense_id"`
	SenseVersionID  string           `json:"sense_version_id"`
	TaskFamily      string           `json:"task_family"`
	Stem            string           `json:"stem"`
	Options         []QuestionOption `json:"options"`
	CorrectOptionID string           `json:"correct_option_id"`
	Explanation     string           `json:"explanation"`
	ParentFormID    *string          `json:"parent_form_id,omitempty"`
}

type QuestionService struct {
	db *sql.DB
}

func NewQuestionService(db *sql.DB) *QuestionService {
	return &QuestionService{db: db}
}

// ValidateQuestion validates structural and reference requirements of a question.
func (s *QuestionService) ValidateQuestion(ctx context.Context, q *QuestionDraft, validTasks []string) error {
	if len(q.Options) != 4 {
		return ErrInvalidOptionCount
	}

	taskValid := false
	for _, t := range validTasks {
		if q.TaskFamily == t {
			taskValid = true
			break
		}
	}
	if !taskValid {
		return ErrInvalidTaskFamily
	}

	// Distinct option IDs, distinct normalized texts, distinct option_identity_ids
	seenIDs := make(map[string]bool)
	seenTexts := make(map[string]bool)
	seenIdentities := make(map[string]bool)
	hasCorrect := false

	for _, o := range q.Options {
		if o.ID == "" || seenIDs[o.ID] {
			return fmt.Errorf("%w: duplicate or empty option id %s", ErrDuplicateOption, o.ID)
		}
		seenIDs[o.ID] = true

		normText := NormalizeText(o.Text)
		if normText == "" || seenTexts[normText] {
			return fmt.Errorf("%w: duplicate option text %q", ErrDuplicateOption, o.Text)
		}
		seenTexts[normText] = true

		if o.OptionIdentityID != "" {
			if seenIdentities[o.OptionIdentityID] {
				return fmt.Errorf("%w: duplicate option_identity_id %s", ErrDuplicateOption, o.OptionIdentityID)
			}
			seenIdentities[o.OptionIdentityID] = true
		}

		if o.ID == q.CorrectOptionID {
			hasCorrect = true
		}

		// Verify option sense exists if present
		if o.SenseID != "" {
			var count int
			err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sense WHERE id = ?", o.SenseID).Scan(&count)
			if err != nil || count == 0 {
				return fmt.Errorf("%w: sense %s", ErrOptionSenseNotFound, o.SenseID)
			}
		}
	}

	if !hasCorrect {
		return ErrInvalidAnswerKey
	}

	// Verify target sense and version exist
	var targetCount int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sense WHERE id = ?", q.SenseID).Scan(&targetCount)
	if err != nil || targetCount == 0 {
		return fmt.Errorf("%w: %s", ErrTargetSenseNotFound, q.SenseID)
	}

	var versionCount int
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sense_version WHERE id = ? AND sense_id = ?", q.SenseVersionID, q.SenseID).Scan(&versionCount)
	if err != nil || versionCount == 0 {
		return fmt.Errorf("target sense_version %s not found for sense %s", q.SenseVersionID, q.SenseID)
	}

	return nil
}

// CreateQuestionVersion determines form deduplication and inserts a new question version.
func (s *QuestionService) CreateQuestionVersion(ctx context.Context, q *QuestionDraft, validTasks []string) (*QuestionVersion, error) {
	if err := s.ValidateQuestion(ctx, q, validTasks); err != nil {
		return nil, err
	}

	now := time.Now().UTC().UnixMicro()
	var version *QuestionVersion

	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		// 1. Gather or compute option identities and canonical keys
		var optionKeys []string
		for i, o := range q.Options {
			var sourceID, sourceVersion string
			// Look up source from sense
			_ = tc.QueryRow(`
				SELECT s.source_id, src.source_version
				FROM sense s
				JOIN source src ON s.source_id = src.id
				WHERE s.id = ?
			`, o.SenseID).Scan(&sourceID, &sourceVersion)

			canonicalKey := ComputeOptionCanonicalKey(o.Language, o.Text, o.SenseID, sourceID, sourceVersion)
			optionKeys = append(optionKeys, canonicalKey)

			identityID := o.OptionIdentityID
			if identityID == "" {
				identityID = core.NewUUID()
			}

			_, err := tc.Exec(`
				INSERT INTO option_identity (id, canonical_key, language, normalized_text, sense_id, source_id, source_version, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(canonical_key) DO UPDATE SET id=id
			`, identityID, canonicalKey, o.Language, NormalizeText(o.Text), o.SenseID, sourceID, sourceVersion, now)
			if err != nil {
				return fmt.Errorf("register option identity: %w", err)
			}
			var actualIdentityID string
			if err := tc.QueryRow("SELECT id FROM option_identity WHERE canonical_key = ?", canonicalKey).Scan(&actualIdentityID); err != nil {
				return fmt.Errorf("lookup registered option identity: %w", err)
			}
			q.Options[i].OptionIdentityID = actualIdentityID
		}

		// 2. Compute form deduplication hash
		dedupHash := ComputeFormDedupHash(q.SenseID, q.TaskFamily, q.Stem, optionKeys)

		// Check if form exists
		var formID string
		err := tc.QueryRow("SELECT id FROM question_form WHERE dedup_hash = ?", dedupHash).Scan(&formID)
		if errors.Is(err, sql.ErrNoRows) {
			if q.ParentFormID != nil && *q.ParentFormID != "" {
				var existingFormID string
				err = tc.QueryRow("SELECT id FROM question_form WHERE id = ?", *q.ParentFormID).Scan(&existingFormID)
				if err != nil {
					return fmt.Errorf("parent form %s not found: %w", *q.ParentFormID, err)
				}
				formID = existingFormID
			} else {
				formID = core.NewUUID()
				_, err = tc.Exec(`
					INSERT INTO question_form (id, sense_id, task_family, dedup_hash, normalized_stem, created_at)
					VALUES (?, ?, ?, ?, ?, ?)
				`, formID, q.SenseID, q.TaskFamily, dedupHash, NormalizeText(q.Stem), now)
				if err != nil {
					return fmt.Errorf("insert question form: %w", err)
				}
			}
		} else if err != nil {
			return fmt.Errorf("query question form: %w", err)
		}

		// 3. Create question version
		optsJSON, err := json.Marshal(q.Options)
		if err != nil {
			return fmt.Errorf("marshal options: %w", err)
		}

		newVersionID := core.NewUUID()
		_, err = tc.Exec(`
			INSERT INTO question_version (id, form_id, sense_id, sense_version_id, task_family, stem, options_json, correct_option_id, explanation, candidate_id, status, revision, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, 'published', 0, ?, ?)
		`, newVersionID, formID, q.SenseID, q.SenseVersionID, q.TaskFamily, q.Stem, string(optsJSON), q.CorrectOptionID, q.Explanation, now, now)
		if err != nil {
			return fmt.Errorf("insert question version: %w", err)
		}

		version = &QuestionVersion{
			ID:              newVersionID,
			FormID:          formID,
			SenseID:         q.SenseID,
			SenseVersionID:  q.SenseVersionID,
			TaskFamily:      q.TaskFamily,
			Stem:            q.Stem,
			Options:         q.Options,
			CorrectOptionID: q.CorrectOptionID,
			Explanation:     q.Explanation,
			Status:          "published",
			Revision:        0,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return version, nil
}
