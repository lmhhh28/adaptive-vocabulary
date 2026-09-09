package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"adaptive-vocabulary/internal/core"
)

type FixtureLexeme struct {
	ID            string `json:"id"`
	CanonicalForm string `json:"canonical_form"`
}

type FixtureSense struct {
	ID           string   `json:"id"`
	VersionID    string   `json:"version_id"`
	LexemeID     string   `json:"lexeme_id"`
	PartOfSpeech string   `json:"part_of_speech"`
	Meaning      string   `json:"meaning"`
	SourceID     string   `json:"source_id"`
	Frequency    *float64 `json:"frequency"`
}

type FixtureAlias struct {
	Form     string `json:"form"`
	LexemeID string `json:"lexeme_id"`
}

type FixtureBank struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	SenseIDs []string `json:"sense_ids"`
}

type FixtureOption struct {
	ID               string `json:"id"`
	Text             string `json:"text"`
	SenseID          string `json:"sense_id"`
	Language         string `json:"language"`
	OptionIdentityID string `json:"option_identity_id"`
}

type FixtureQuestion struct {
	ID              string          `json:"id"`
	FormID          string          `json:"form_id"`
	SenseID         string          `json:"sense_id"`
	SenseVersionID  string          `json:"sense_version_id"`
	TaskFamily      string          `json:"task_family"`
	Stem            string          `json:"stem"`
	Options         []FixtureOption `json:"options"`
	CorrectOptionID string          `json:"correct_option_id"`
	Explanation     string          `json:"explanation"`
}

type ContentFixture struct {
	SchemaVersion int               `json:"schema_version"`
	SourceID      string            `json:"source_id"`
	SourceVersion string            `json:"source_version"`
	Purpose       string            `json:"purpose"`
	Lexemes       []FixtureLexeme   `json:"lexemes"`
	Senses        []FixtureSense    `json:"senses"`
	Aliases       []FixtureAlias    `json:"aliases"`
	Banks         []FixtureBank     `json:"banks"`
	Questions     []FixtureQuestion `json:"questions"`
}

type ExamFixture struct {
	SchemaVersion int               `json:"schema_version"`
	SourceID      string            `json:"source_id"`
	SourceVersion string            `json:"source_version"`
	Questions     []FixtureQuestion `json:"questions"`
}

// FindFixturePath searches candidate locations for a fixture file.
func FindFixturePath(filename string) (string, error) {
	candidates := []string{
		filepath.Join("fixtures", filename),
		filepath.Join("..", "fixtures", filename),
		filepath.Join("..", "..", "fixtures", filename),
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates,
			filepath.Join(filepath.Dir(exe), "fixtures", filename),
			filepath.Join(filepath.Dir(exe), "..", "fixtures", filename),
		)
	}
	for _, c := range candidates {
		if abs, err := filepath.Abs(c); err == nil {
			if info, err := os.Stat(abs); err == nil && !info.IsDir() {
				return abs, nil
			}
		}
	}
	return "", fmt.Errorf("fixture file %q not found", filename)
}

// ApplySeed loads fixed seed data into the database if requested.
func ApplySeed(ctx context.Context, db *sql.DB, seedName string) error {
	if seedName == "" {
		return nil
	}
	if seedName != "core" && seedName != "exam-conflict" {
		return fmt.Errorf("unknown seed dataset %q: valid seeds are 'core' or 'exam-conflict'", seedName)
	}

	// Check if already seeded or non-empty
	var existingLexemes int
	var existingQuestions int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM lexeme").Scan(&existingLexemes)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check existing lexemes: %w", err)
	}
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM question_version").Scan(&existingQuestions)

	if existingLexemes > 0 {
		// Verify idempotency
		if seedName == "core" && existingLexemes == 8 && existingQuestions == 14 {
			return nil // Already cleanly seeded with core
		}
		if seedName == "exam-conflict" && existingLexemes == 8 && existingQuestions == 13 {
			return nil // Already cleanly seeded with exam-conflict
		}
		return fmt.Errorf("cannot apply seed %q: database is already populated with %d lexemes and %d questions",
			seedName, existingLexemes, existingQuestions)
	}

	// 1. Read base content fixture
	contentPath, err := FindFixturePath("content-v1.json")
	if err != nil {
		return fmt.Errorf("locate content-v1.json: %w", err)
	}
	contentData, err := os.ReadFile(contentPath)
	if err != nil {
		return fmt.Errorf("read content-v1.json: %w", err)
	}
	var base ContentFixture
	if err := json.Unmarshal(contentData, &base); err != nil {
		return fmt.Errorf("unmarshal content-v1.json: %w", err)
	}

	questionsToImport := base.Questions
	sourcesToImport := []Source{
		{
			ID:            base.SourceID,
			Name:          "Project Authored Fixture",
			Location:      "fixtures/content-v1.json",
			SourceVersion: base.SourceVersion,
			License:       "MIT",
			UsageScope:    "Development and contract verification",
		},
	}

	// 2. If seed is core, read exam assembly supplement fixture
	if seedName == "core" {
		examPath, err := FindFixturePath("exam-assembly-v1.json")
		if err != nil {
			return fmt.Errorf("locate exam-assembly-v1.json: %w", err)
		}
		examData, err := os.ReadFile(examPath)
		if err != nil {
			return fmt.Errorf("read exam-assembly-v1.json: %w", err)
		}
		var exam ExamFixture
		if err := json.Unmarshal(examData, &exam); err != nil {
			return fmt.Errorf("unmarshal exam-assembly-v1.json: %w", err)
		}
		questionsToImport = append(questionsToImport, exam.Questions...)
		sourcesToImport = append(sourcesToImport, Source{
			ID:            exam.SourceID,
			Name:          "Project Authored Exam Supplement",
			Location:      "fixtures/exam-assembly-v1.json",
			SourceVersion: exam.SourceVersion,
			License:       "MIT",
			UsageScope:    "Development exam assembly cases",
		})
	}

	now := time.Now().UTC().UnixMicro()

	// 3. Import in a single transaction
	return core.RunInTx(ctx, db, func(tc *core.TxnContext) error {
		// Sources
		for _, s := range sourcesToImport {
			_, err := tc.Exec(`
				INSERT INTO source (id, name, location, source_version, license, usage_scope, checksum, revision, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, NULL, 0, ?, ?)
				ON CONFLICT(id) DO NOTHING
			`, s.ID, s.Name, s.Location, s.SourceVersion, s.License, s.UsageScope, now, now)
			if err != nil {
				return fmt.Errorf("insert source %s: %w", s.ID, err)
			}
		}

		// Lexemes
		for _, l := range base.Lexemes {
			canonical := NormalizeLexeme(l.CanonicalForm)
			_, err := tc.Exec(`
				INSERT INTO lexeme (id, canonical_form, created_at)
				VALUES (?, ?, ?)
				ON CONFLICT(id) DO NOTHING
			`, l.ID, canonical, now)
			if err != nil {
				return fmt.Errorf("insert lexeme %s: %w", l.ID, err)
			}
		}

		// Aliases
		for _, a := range base.Aliases {
			form := NormalizeLexeme(a.Form)
			_, err := tc.Exec(`
				INSERT INTO lexeme_alias (id, form, lexeme_id, created_at)
				VALUES (?, ?, ?, ?)
				ON CONFLICT(form) DO NOTHING
			`, core.NewUUID(), form, a.LexemeID, now)
			if err != nil {
				return fmt.Errorf("insert alias %s: %w", a.Form, err)
			}
		}

		// Senses & SenseVersions
		for _, s := range base.Senses {
			_, err := tc.Exec(`
				INSERT INTO sense (id, lexeme_id, part_of_speech, source_id, created_at)
				VALUES (?, ?, ?, ?, ?)
				ON CONFLICT(id) DO NOTHING
			`, s.ID, s.LexemeID, s.PartOfSpeech, s.SourceID, now)
			if err != nil {
				return fmt.Errorf("insert sense %s: %w", s.ID, err)
			}

			_, err = tc.Exec(`
				INSERT INTO sense_version (id, sense_id, meaning, frequency, version_number, created_at)
				VALUES (?, ?, ?, ?, 1, ?)
				ON CONFLICT(sense_id, version_number) DO NOTHING
			`, s.VersionID, s.ID, s.Meaning, s.Frequency, now)
			if err != nil {
				return fmt.Errorf("insert sense_version %s: %w", s.VersionID, err)
			}
		}

		// Banks & BankSense
		for _, b := range base.Banks {
			_, err := tc.Exec(`
				INSERT INTO bank (id, name, note, archived, revision, created_at, updated_at)
				VALUES (?, ?, '', 0, 0, ?, ?)
				ON CONFLICT(id) DO NOTHING
			`, b.ID, b.Name, now, now)
			if err != nil {
				return fmt.Errorf("insert bank %s: %w", b.ID, err)
			}

			for _, senseID := range b.SenseIDs {
				_, err := tc.Exec(`
					INSERT INTO bank_sense (bank_id, sense_id, added_at)
					VALUES (?, ?, ?)
					ON CONFLICT(bank_id, sense_id) DO NOTHING
				`, b.ID, senseID, now)
				if err != nil {
					return fmt.Errorf("insert bank_sense (%s, %s): %w", b.ID, senseID, err)
				}
			}
		}

		// Questions, Forms, OptionIdentities, Versions
		for _, q := range questionsToImport {
			var optionKeys []string
			for _, o := range q.Options {
				canonicalKey := ComputeOptionCanonicalKey(o.Language, o.Text, o.SenseID, base.SourceID, base.SourceVersion)
				optionKeys = append(optionKeys, canonicalKey)

				// Register option identity
				_, err := tc.Exec(`
					INSERT INTO option_identity (id, canonical_key, language, normalized_text, sense_id, source_id, source_version, created_at)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?)
					ON CONFLICT(canonical_key) DO UPDATE SET id=excluded.id
				`, o.OptionIdentityID, canonicalKey, o.Language, NormalizeText(o.Text), o.SenseID, base.SourceID, base.SourceVersion, now)
				if err != nil {
					return fmt.Errorf("insert option_identity %s: %w", o.OptionIdentityID, err)
				}
			}

			dedupHash := ComputeFormDedupHash(q.SenseID, q.TaskFamily, q.Stem, optionKeys)
			_, err := tc.Exec(`
				INSERT INTO question_form (id, sense_id, task_family, dedup_hash, normalized_stem, created_at)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(dedup_hash) DO NOTHING
			`, q.FormID, q.SenseID, q.TaskFamily, dedupHash, NormalizeText(q.Stem), now)
			if err != nil {
				return fmt.Errorf("insert question_form %s: %w", q.FormID, err)
			}

			optsJSON, err := json.Marshal(q.Options)
			if err != nil {
				return fmt.Errorf("marshal options for question %s: %w", q.ID, err)
			}

			_, err = tc.Exec(`
				INSERT INTO question_version (id, form_id, sense_id, sense_version_id, task_family, stem, options_json, correct_option_id, explanation, candidate_id, status, revision, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, 'published', 0, ?, ?)
				ON CONFLICT(id) DO NOTHING
			`, q.ID, q.FormID, q.SenseID, q.SenseVersionID, q.TaskFamily, q.Stem, string(optsJSON), q.CorrectOptionID, q.Explanation, now, now)
			if err != nil {
				return fmt.Errorf("insert question_version %s: %w", q.ID, err)
			}
		}

		return nil
	})
}
