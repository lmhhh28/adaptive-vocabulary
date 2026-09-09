//go:build e2e

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"adaptive-vocabulary/internal/content"
	"adaptive-vocabulary/internal/core"
	"adaptive-vocabulary/internal/learning"
	"adaptive-vocabulary/internal/platform"
	"adaptive-vocabulary/internal/selection"
)

func runExtraCommands(cmd string, args []string) bool {
	if cmd == "learning" {
		runLearningE2E(args)
		return true
	}
	return false
}

func runLearningE2E(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "learning command requires a subcommand (e.g. verify-trace, select, labels)\n")
		os.Exit(1)
	}

	sub := args[0]
	switch sub {
	case "verify-trace":
		runVerifyTrace(args[1:])
	case "select":
		runSelectCLI(args[1:])
	case "labels":
		runLabelsCLI(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown learning subcommand: %s\n", sub)
		os.Exit(1)
	}
}

type TraceStep struct {
	Step                 int                    `json:"step"`
	Kind                 string                 `json:"kind"` // session, reserve, attempt, select
	SessionID            string                 `json:"session_id"`
	SessionKind          string                 `json:"session_kind"`
	Status               string                 `json:"status"`
	InstanceID           string                 `json:"instance_id"`
	Ordinal              int                    `json:"ordinal"`
	NormalPosition       *int                   `json:"normal_position"`
	QuestionVersionID    string                 `json:"question_version_id"`
	SenseID              string                 `json:"sense_id"`
	LexemeID             string                 `json:"lexeme_id"`
	FormID               string                 `json:"form_id"`
	ObservedAt           int64                  `json:"observed_at"`
	CreatedAt            int64                  `json:"created_at"`
	Attempt              *learning.AttemptEvent `json:"attempt"`
	BankID               string                 `json:"bank_id"`
	NowMicro             int64                  `json:"now_micro"`
	ExpectedStatus       string                 `json:"expected_status"`
	ExpectedGroup        string                 `json:"expected_group"`
	ExpectedSenseID      string                 `json:"expected_sense_id"`
	ExpectedFormID       string                 `json:"expected_form_id"`
	ExpectedIsCorrection *bool                  `json:"expected_is_correction"`
}

type TraceFixture struct {
	Description string      `json:"description"`
	Seed        string      `json:"seed"`
	Policy      string      `json:"policy"`
	Steps       []TraceStep `json:"steps"`
}

func runVerifyTrace(args []string) {
	fs := flag.NewFlagSet("learning verify-trace", flag.ExitOnError)
	inputFile := fs.String("input", "", "Path to trace input JSON file")
	outputDir := fs.String("output", "", "Path to output directory for isolated DB and summary")
	_ = fs.Parse(args)

	if *inputFile == "" || *outputDir == "" {
		fmt.Fprintln(os.Stderr, "Usage: avocab-e2e learning verify-trace --input <file> --output <dir>")
		os.Exit(1)
	}

	absInputDir, err := filepath.Abs(*inputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to resolve input path %q: %v\n", *inputFile, err)
		os.Exit(1)
	}
	absOutputDir, err := filepath.Abs(*outputDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to resolve output path %q: %v\n", *outputDir, err)
		os.Exit(1)
	}

	rawFixture, err := os.ReadFile(absInputDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read input file: %v\n", err)
		os.Exit(1)
	}

	var fixture TraceFixture
	if err := json.Unmarshal(rawFixture, &fixture); err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse trace fixture: %v\n", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(absOutputDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create output directory: %v\n", err)
		os.Exit(1)
	}

	dbPath := filepath.Join(absOutputDir, "trace.db")
	_ = os.Remove(dbPath) // start fresh

	db, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open sqlite: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if _, err := platform.RunMigrations(db); err != nil {
		fmt.Fprintf(os.Stderr, "failed to run migrations: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Apply seed if declared
	if fixture.Seed != "" {
		if err := content.ApplySeed(ctx, db, fixture.Seed); err != nil {
			fmt.Fprintf(os.Stderr, "failed to apply seed %q: %v\n", fixture.Seed, err)
			os.Exit(1)
		}
	}

	// Resolve policy
	policyPath := fixture.Policy
	if policyPath == "" {
		policyPath = "config/policy-v2.json"
	}
	policyCfg, err := learning.LoadFSRSPolicyFromPath(policyPath)
	if err != nil {
		candidates := []string{
			policyPath,
			filepath.Join("..", policyPath),
			filepath.Join("..", "..", policyPath),
		}
		for _, c := range candidates {
			if cfg, err2 := learning.LoadFSRSPolicyFromPath(c); err2 == nil {
				policyCfg = cfg
				break
			}
		}
		if policyCfg == nil {
			fmt.Fprintf(os.Stderr, "failed to load fsrs policy from %q: %v\n", policyPath, err)
			os.Exit(1)
		}
	}

	svc := learning.NewLearningService(db, *policyCfg, "observation-v2", "v1")

	// Apply steps sequentially
	for _, step := range fixture.Steps {
		if step.Kind == "select" {
			selCfg, err := selection.LoadSelectionConfigFromPath(policyPath)
			if err != nil {
				candidates := []string{
					policyPath,
					filepath.Join("..", policyPath),
					filepath.Join("..", "..", policyPath),
				}
				for _, c := range candidates {
					if cfg, err2 := selection.LoadSelectionConfigFromPath(c); err2 == nil {
						selCfg = cfg
						break
					}
				}
				if selCfg == nil {
					defCfg := selection.DefaultSelectionConfig()
					selCfg = &defCfg
				}
			}
			selector := selection.NewSelector(db, *selCfg)
			pos := 1
			if step.NormalPosition != nil && *step.NormalPosition > 0 {
				pos = *step.NormalPosition
			}
			selRes, err := selector.SelectNextQuestion(ctx, selection.SelectionRequest{
				SessionID:         step.SessionID,
				BankID:            step.BankID,
				NormalPosition:    pos,
				ProjectionVersion: "v1",
				NowMicro:          step.NowMicro,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "step %d select failed: %v\n", step.Step, err)
				os.Exit(1)
			}
			if step.ExpectedStatus != "" && selRes.Status != step.ExpectedStatus {
				fmt.Fprintf(os.Stderr, "step %d expected status %s, got %s\n", step.Step, step.ExpectedStatus, selRes.Status)
				os.Exit(1)
			}
			if step.ExpectedGroup != "" && selRes.CandidateGroup != step.ExpectedGroup {
				fmt.Fprintf(os.Stderr, "step %d expected group %s, got %s\n", step.Step, step.ExpectedGroup, selRes.CandidateGroup)
				os.Exit(1)
			}
			if step.ExpectedSenseID != "" && selRes.SenseID != step.ExpectedSenseID {
				fmt.Fprintf(os.Stderr, "step %d expected sense %s, got %s\n", step.Step, step.ExpectedSenseID, selRes.SenseID)
				os.Exit(1)
			}
			if step.ExpectedFormID != "" && selRes.FormID != step.ExpectedFormID {
				fmt.Fprintf(os.Stderr, "step %d expected form %s, got %s\n", step.Step, step.ExpectedFormID, selRes.FormID)
				os.Exit(1)
			}
			if step.ExpectedIsCorrection != nil && selRes.IsCorrection != *step.ExpectedIsCorrection {
				fmt.Fprintf(os.Stderr, "step %d expected is_correction %v, got %v\n", step.Step, *step.ExpectedIsCorrection, selRes.IsCorrection)
				os.Exit(1)
			}
			continue
		}

		err := core.RunInTx(ctx, db, func(tc *core.TxnContext) error {
			switch step.Kind {
			case "session":
				sKind := step.SessionKind
				if sKind == "" {
					sKind = "practice"
				}
				sStatus := step.Status
				if sStatus == "" {
					sStatus = "open"
				}
				createdAt := step.CreatedAt
				if createdAt == 0 {
					createdAt = step.ObservedAt
				}
				query := `
					INSERT INTO session (id, kind, status, created_at, updated_at)
					VALUES (?, ?, ?, ?, ?)
					ON CONFLICT (id) DO UPDATE SET status = excluded.status, updated_at = excluded.updated_at
				`
				_, err := tc.Exec(query, step.SessionID, sKind, sStatus, createdAt, createdAt)
				return err

			case "reserve":
				// Query version details for snapshot
				var stem, optionsJSON, correctOptionID string
				queryQV := `
					SELECT stem, options_json, correct_option_id
					FROM question_version
					WHERE id = ?
				`
				err := tc.QueryRow(queryQV, step.QuestionVersionID).Scan(&stem, &optionsJSON, &correctOptionID)
				if err != nil {
					return fmt.Errorf("lookup question_version %s: %w", step.QuestionVersionID, err)
				}

				snapshot := map[string]any{
					"stem":              stem,
					"options":           json.RawMessage(optionsJSON),
					"correct_option_id": correctOptionID,
				}
				snapshotBytes, _ := json.Marshal(snapshot)

				queryInst := `
					INSERT INTO question_instance (id, session_id, ordinal, normal_position, question_version_id, snapshot_json, created_at)
					VALUES (?, ?, ?, ?, ?, ?, ?)
				`
				_, err = tc.Exec(queryInst, step.InstanceID, step.SessionID, step.Ordinal, step.NormalPosition, step.QuestionVersionID, string(snapshotBytes), step.ObservedAt)
				if err != nil {
					return fmt.Errorf("insert question_instance: %w", err)
				}

				// Record reserved exposure
				exp := &learning.ExposureEvent{
					SourceInstanceID: step.InstanceID,
					Kind:             learning.ExposureReserved,
					SenseID:          step.SenseID,
					LexemeID:         step.LexemeID,
					FormID:           step.FormID,
					ObservedAt:       step.ObservedAt,
				}
				return learning.RecordExposure(tc, exp)

			case "attempt":
				if step.Attempt == nil {
					return fmt.Errorf("attempt details missing in step %d", step.Step)
				}

				// Query instance snapshot to extract options
				var snapshotStr string
				querySnap := `SELECT snapshot_json FROM question_instance WHERE id = ?`
				if err := tc.QueryRow(querySnap, step.Attempt.InstanceID).Scan(&snapshotStr); err != nil {
					return fmt.Errorf("lookup instance %s snapshot: %w", step.Attempt.InstanceID, err)
				}

				var snapData struct {
					Options []struct {
						ID               string `json:"id"`
						OptionIdentityID string `json:"option_identity_id"`
						IsCorrect        bool   `json:"is_correct"`
					} `json:"options"`
					CorrectOptionID string `json:"correct_option_id"`
				}
				_ = json.Unmarshal([]byte(snapshotStr), &snapData)

				options := make([]learning.InstanceOption, len(snapData.Options))
				for i, opt := range snapData.Options {
					isCorrect := opt.IsCorrect || (snapData.CorrectOptionID != "" && opt.ID == snapData.CorrectOptionID)
					options[i] = learning.InstanceOption{
						ID:               opt.ID,
						OptionIdentityID: opt.OptionIdentityID,
						IsCorrect:        isCorrect,
					}
				}

				// Apply attempt
				_, err := svc.ApplyAttempt(tc, step.Attempt, options)
				if err != nil {
					return fmt.Errorf("apply attempt %s: %w", step.Attempt.ID, err)
				}

				// Query lexeme and form for feedback_available exposure
				var lexemeID, formID string
				queryMeta := `
					SELECT s.lexeme_id, qv.form_id
					FROM question_instance qi
					JOIN question_version qv ON qv.id = qi.question_version_id
					JOIN sense s ON s.id = qv.sense_id
					WHERE qi.id = ?
				`
				_ = tc.QueryRow(queryMeta, step.Attempt.InstanceID).Scan(&lexemeID, &formID)

				exp := &learning.ExposureEvent{
					SourceInstanceID: step.Attempt.InstanceID,
					Kind:             learning.ExposureFeedbackAvailable,
					SenseID:          step.Attempt.SenseID,
					LexemeID:         lexemeID,
					FormID:           formID,
					ObservedAt:       step.Attempt.ObservedAt,
				}
				return learning.RecordExposure(tc, exp)

			default:
				return fmt.Errorf("unknown step kind %q", step.Kind)
			}
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to apply step %d: %v\n", step.Step, err)
			os.Exit(1)
		}
	}

	// Replay projections
	if err := svc.ReplayProjections(ctx, "replay_v1", 0); err != nil {
		fmt.Fprintf(os.Stderr, "failed to replay projections: %v\n", err)
		os.Exit(1)
	}

	// Compare projections
	identical, diff, err := learning.CompareProjections(ctx, db, "v1", "replay_v1")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to compare projections: %v\n", err)
		os.Exit(1)
	}
	if !identical {
		fmt.Fprintf(os.Stderr, "replay verification mismatch: %s\n", diff)
		os.Exit(1)
	}

	// Gather metrics
	var reviewsCount, evCount, memCount, confCount int
	_ = db.QueryRow("SELECT count(*) FROM review_event").Scan(&reviewsCount)
	_ = db.QueryRow("SELECT count(*) FROM evidence_projection WHERE projection_version = 'v1'").Scan(&evCount)
	_ = db.QueryRow("SELECT count(*) FROM memory_projection WHERE projection_version = 'v1'").Scan(&memCount)
	_ = db.QueryRow("SELECT count(*) FROM confusion_projection WHERE projection_version = 'v1'").Scan(&confCount)

	summary := map[string]any{
		"status":                "ok",
		"operation":             "verify-trace",
		"input":                 *inputFile,
		"output_dir":            *outputDir,
		"steps_applied":         len(fixture.Steps),
		"reviews_count":         reviewsCount,
		"fsrs_replay":           "identical",
		"evidence_projections":  evCount,
		"memory_projections":    memCount,
		"confusion_projections": confCount,
	}

	summaryBytes, _ := json.MarshalIndent(summary, "", "  ")
	_ = os.WriteFile(filepath.Join(absOutputDir, "summary.json"), summaryBytes, 0644)
	fmt.Println(string(summaryBytes))
}

func runSelectCLI(args []string) {
	fs := flag.NewFlagSet("learning select", flag.ExitOnError)
	dbPath := fs.String("db", "", "Path to SQLite database")
	policyPath := fs.String("policy", "config/policy-v2.json", "Path to policy JSON")
	bankID := fs.String("bank", "", "Bank ID")
	sessionID := fs.String("session", "", "Session ID")
	pos := fs.Int("pos", 1, "Normal position (1-based)")
	nowMicro := fs.Int64("now", 0, "Current time in microseconds UTC")
	_ = fs.Parse(args)

	if *dbPath == "" || *bankID == "" || *sessionID == "" {
		fmt.Fprintln(os.Stderr, "Usage: avocab-e2e learning select --db <db> --bank <bank_id> --session <sess_id> [--pos <pos>] [--now <now>]")
		os.Exit(1)
	}

	db, err := sql.Open("sqlite", *dbPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	selCfg, err := selection.LoadSelectionConfigFromPath(*policyPath)
	if err != nil {
		def := selection.DefaultSelectionConfig()
		selCfg = &def
	}

	selector := selection.NewSelector(db, *selCfg)
	res, err := selector.SelectNextQuestion(context.Background(), selection.SelectionRequest{
		SessionID:         *sessionID,
		BankID:            *bankID,
		NormalPosition:    *pos,
		ProjectionVersion: "v1",
		NowMicro:          *nowMicro,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "select next error: %v\n", err)
		os.Exit(1)
	}

	out, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(out))
}

func runLabelsCLI(args []string) {
	fs := flag.NewFlagSet("learning labels", flag.ExitOnError)
	dbPath := fs.String("db", "", "Path to SQLite database")
	instID := fs.String("instance", "", "Instance ID to exclude")
	formID := fs.String("form", "", "Form ID")
	lexemeID := fs.String("lexeme", "", "Lexeme ID")
	cutoff := fs.Int64("cutoff", 0, "Cutoff observed_at microsecond")
	seq := fs.Int64("seq", 0, "Cutoff sequence")
	_ = fs.Parse(args)

	if *dbPath == "" || *formID == "" || *lexemeID == "" {
		fmt.Fprintln(os.Stderr, "Usage: avocab-e2e learning labels --db <db> --instance <inst> --form <form> --lexeme <lexeme> --cutoff <cutoff> --seq <seq>")
		os.Exit(1)
	}

	db, err := sql.Open("sqlite", *dbPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	tx, err := db.Begin()
	if err != nil {
		fmt.Fprintf(os.Stderr, "begin tx: %v\n", err)
		os.Exit(1)
	}
	defer tx.Rollback()

	hasSameForm, hasRecentRelated, err := learning.DeriveExposureLabels(context.Background(), tx, *instID, *formID, *lexemeID, *cutoff, *seq)
	if err != nil {
		fmt.Fprintf(os.Stderr, "derive labels error: %v\n", err)
		os.Exit(1)
	}

	out, _ := json.Marshal(map[string]any{
		"has_same_form":      hasSameForm,
		"has_recent_related": hasRecentRelated,
	})
	fmt.Println(string(out))
}
