package learning

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"

	"adaptive-vocabulary/internal/core"
)

// ReplayProjections rebuilds evidence, memory, and confusion projections from raw events up to cutoffSequence.
func (s *LearningService) ReplayProjections(ctx context.Context, targetProjectionVersion string, cutoffSequence int64) error {
	return core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		// 1. Clear existing projections for targetProjectionVersion
		if _, err := tc.Exec("DELETE FROM evidence_projection WHERE projection_version = ?", targetProjectionVersion); err != nil {
			return fmt.Errorf("clear evidence projection: %w", err)
		}
		if _, err := tc.Exec("DELETE FROM memory_projection WHERE projection_version = ?", targetProjectionVersion); err != nil {
			return fmt.Errorf("clear memory projection: %w", err)
		}
		if _, err := tc.Exec("DELETE FROM confusion_projection WHERE projection_version = ?", targetProjectionVersion); err != nil {
			return fmt.Errorf("clear confusion projection: %w", err)
		}

		// 2. Query attempts up to cutoffSequence
		queryAttempts := `
			SELECT a.id, a.instance_id, a.session_id, a.sense_id, a.task_family, a.source,
			       a.purpose, a.response_history, a.selected_option_id, a.final_option_id,
			       a.first_response_event_id, a.final_response_event_id, a.revision_count,
			       a.client_active_duration_ms, a.interruption_flags, a.grading_version,
			       a.is_correct, a.observed_at, a.event_sequence, a.created_at,
			       qi.snapshot_json
			FROM attempt_event a
			JOIN question_instance qi ON qi.id = a.instance_id
			WHERE (? = 0 OR a.event_sequence <= ?)
			ORDER BY a.observed_at ASC, a.event_sequence ASC, a.id ASC
		`
		rows, err := tc.Query(queryAttempts, cutoffSequence, cutoffSequence)
		if err != nil {
			return fmt.Errorf("query attempts for replay: %w", err)
		}
		defer rows.Close()

		type attemptWithSnapshot struct {
			attempt  AttemptEvent
			snapshot string
		}
		var attempts []attemptWithSnapshot

		for rows.Next() {
			var item attemptWithSnapshot
			var selectedOpt, finalOpt, firstResp, finalResp sql.NullString
			var clientDuration sql.NullInt64
			var interruptionJSON sql.NullString
			var isCorrectInt int

			err := rows.Scan(
				&item.attempt.ID,
				&item.attempt.InstanceID,
				&item.attempt.SessionID,
				&item.attempt.SenseID,
				&item.attempt.TaskFamily,
				&item.attempt.Source,
				&item.attempt.Purpose,
				&item.attempt.ResponseHistory,
				&selectedOpt,
				&finalOpt,
				&firstResp,
				&finalResp,
				&item.attempt.RevisionCount,
				&clientDuration,
				&interruptionJSON,
				&item.attempt.GradingVersion,
				&isCorrectInt,
				&item.attempt.ObservedAt,
				&item.attempt.EventSequence,
				&item.attempt.CreatedAt,
				&item.snapshot,
			)
			if err != nil {
				return fmt.Errorf("scan attempt for replay: %w", err)
			}

			if selectedOpt.Valid {
				item.attempt.SelectedOptionID = &selectedOpt.String
			}
			if finalOpt.Valid {
				item.attempt.FinalOptionID = &finalOpt.String
			}
			if firstResp.Valid {
				item.attempt.FirstResponseEventID = &firstResp.String
			}
			if finalResp.Valid {
				item.attempt.FinalResponseEventID = &finalResp.String
			}
			if clientDuration.Valid {
				item.attempt.ClientActiveDurationMs = &clientDuration.Int64
			}
			if interruptionJSON.Valid {
				_ = json.Unmarshal([]byte(interruptionJSON.String), &item.attempt.InterruptionFlags)
			}
			item.attempt.IsCorrect = isCorrectInt == 1

			attempts = append(attempts, item)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate replay attempts: %w", err)
		}

		replayService := &LearningService{
			db:                       s.db,
			scheduler:                s.scheduler,
			policy:                   s.policy,
			observationPolicyVersion: s.observationPolicyVersion,
			projectionVersion:        targetProjectionVersion,
		}

		for _, item := range attempts {
			// Extract options from question instance snapshot
			var snapshotData struct {
				Options []struct {
					ID               string `json:"id"`
					OptionIdentityID string `json:"option_identity_id"`
					IsCorrect        bool   `json:"is_correct"`
				} `json:"options"`
				CorrectOptionID string `json:"correct_option_id"`
			}
			_ = json.Unmarshal([]byte(item.snapshot), &snapshotData)

			options := make([]InstanceOption, len(snapshotData.Options))
			for i, o := range snapshotData.Options {
				isCorrect := o.IsCorrect || (snapshotData.CorrectOptionID != "" && o.ID == snapshotData.CorrectOptionID)
				options[i] = InstanceOption{
					ID:               o.ID,
					OptionIdentityID: o.OptionIdentityID,
					IsCorrect:        isCorrect,
				}
			}

			// Check event revisions
			var latestRevKind string
			err := tc.QueryRow(
				"SELECT revision_kind FROM event_revision WHERE (event_id = ? OR event_id = ?) ORDER BY created_at DESC, id DESC LIMIT 1",
				item.attempt.ID,
				item.attempt.InstanceID,
			).Scan(&latestRevKind)
			isDisputed := false
			if err == nil {
				isDisputed = (latestRevKind == "disputed" || latestRevKind == "invalidate")
			} else if err != sql.ErrNoRows {
				return fmt.Errorf("check revision in replay: %w", err)
			}

			decision := ClassifyEligibility(&item.attempt, isDisputed)

			if decision.FSRSReview {
				if _, err := replayService.applyFSRS(tc, &item.attempt); err != nil {
					return fmt.Errorf("replay apply fsrs: %w", err)
				}
			}

			if err := replayService.updateEvidence(tc, &item.attempt, decision); err != nil {
				return fmt.Errorf("replay update evidence: %w", err)
			}

			if err := replayService.updateConfusion(tc, &item.attempt, options, decision); err != nil {
				return fmt.Errorf("replay update confusion: %w", err)
			}
		}

		return nil
	})
}

// CompareProjections verifies that two projection versions have identical evidence, memory, and confusion states.
func CompareProjections(ctx context.Context, db *sql.DB, versionA, versionB string) (bool, string, error) {
	// 1. Compare evidence
	queryEv := `
		SELECT sense_id, task_family, ordinary_samples, ordinary_correct,
		       normal_samples, normal_correct, early_review_samples, early_review_correct,
		       exam_single_samples, exam_single_correct, correction_samples, correction_correct,
		       exam_revised_samples, exam_telemetry_incomplete_samples, omitted_samples,
		       last_ordinary_observed_at, last_ordinary_correct, earliest_ordinary_observed_at,
		       needs_consolidation
		FROM evidence_projection
		WHERE projection_version = ?
		ORDER BY sense_id, task_family
	`
	loadEv := func(v string) (map[string]map[string]any, error) {
		rows, err := db.QueryContext(ctx, queryEv, v)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		res := make(map[string]map[string]any)
		for rows.Next() {
			var senseID, taskFamily string
			var ordS, ordC, normS, normC, earlyS, earlyC, examS, examC, corrS, corrC, revS, uncS, omitS, needsC int
			var lastObs, earObs sql.NullInt64
			var lastCorr sql.NullInt64
			if err := rows.Scan(&senseID, &taskFamily, &ordS, &ordC, &normS, &normC, &earlyS, &earlyC, &examS, &examC, &corrS, &corrC, &revS, &uncS, &omitS, &lastObs, &lastCorr, &earObs, &needsC); err != nil {
				return nil, err
			}
			key := senseID + "|" + taskFamily
			res[key] = map[string]any{
				"ordinary_samples": ordS, "ordinary_correct": ordC,
				"normal_samples": normS, "normal_correct": normC,
				"early_review_samples": earlyS, "early_review_correct": earlyC,
				"exam_single_samples": examS, "exam_single_correct": examC,
				"correction_samples": corrS, "correction_correct": corrC,
				"exam_revised_samples": revS, "exam_telemetry_incomplete_samples": uncS,
				"omitted_samples": omitS, "needs_consolidation": needsC,
				"last_observed": lastObs.Int64, "last_correct": lastCorr.Int64, "earliest_observed": earObs.Int64,
			}
		}
		return res, rows.Err()
	}

	evA, err := loadEv(versionA)
	if err != nil {
		return false, "", fmt.Errorf("load evidence A: %w", err)
	}
	evB, err := loadEv(versionB)
	if err != nil {
		return false, "", fmt.Errorf("load evidence B: %w", err)
	}
	if !reflect.DeepEqual(evA, evB) {
		return false, fmt.Sprintf("evidence mismatch: versionA has %d records, versionB has %d records", len(evA), len(evB)), nil
	}

	// 2. Compare memory
	queryMem := `
		SELECT sense_id, task_family, fsrs_state, fsrs_step, round(stability, 4), round(difficulty, 4),
		       due_at, last_review_at, reps, lapses, round(elapsed_days, 4), round(scheduled_days, 4)
		FROM memory_projection
		WHERE projection_version = ?
		ORDER BY sense_id, task_family
	`
	loadMem := func(v string) (map[string]map[string]any, error) {
		rows, err := db.QueryContext(ctx, queryMem, v)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		res := make(map[string]map[string]any)
		for rows.Next() {
			var senseID, taskFamily string
			var state, step, reps, lapses int
			var stab, diff, elDays, schDays float64
			var dueAt int64
			var lastRev sql.NullInt64
			if err := rows.Scan(&senseID, &taskFamily, &state, &step, &stab, &diff, &dueAt, &lastRev, &reps, &lapses, &elDays, &schDays); err != nil {
				return nil, err
			}
			key := senseID + "|" + taskFamily
			res[key] = map[string]any{
				"state": state, "step": step, "stability": stab, "difficulty": diff,
				"due_at": dueAt, "last_review_at": lastRev.Int64, "reps": reps, "lapses": lapses,
				"elapsed_days": elDays, "scheduled_days": schDays,
			}
		}
		return res, rows.Err()
	}

	memA, err := loadMem(versionA)
	if err != nil {
		return false, "", fmt.Errorf("load memory A: %w", err)
	}
	memB, err := loadMem(versionB)
	if err != nil {
		return false, "", fmt.Errorf("load memory B: %w", err)
	}
	if !reflect.DeepEqual(memA, memB) {
		return false, fmt.Sprintf("memory mismatch: versionA has %d records, versionB has %d records", len(memA), len(memB)), nil
	}

	// 3. Compare confusion
	queryConf := `
		SELECT target_sense_id, task_family, distractor_identity_id, stat_group,
		       eligible_offered_count, wrong_selected_count, last_offered_at, last_selected_at
		FROM confusion_projection
		WHERE projection_version = ?
		ORDER BY target_sense_id, task_family, distractor_identity_id, stat_group
	`
	loadConf := func(v string) (map[string]map[string]any, error) {
		rows, err := db.QueryContext(ctx, queryConf, v)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		res := make(map[string]map[string]any)
		for rows.Next() {
			var targetSenseID, taskFamily, distID, statGroup string
			var offered, wrong int
			var lastOff, lastSel sql.NullInt64
			if err := rows.Scan(&targetSenseID, &taskFamily, &distID, &statGroup, &offered, &wrong, &lastOff, &lastSel); err != nil {
				return nil, err
			}
			key := targetSenseID + "|" + taskFamily + "|" + distID + "|" + statGroup
			res[key] = map[string]any{
				"eligible_offered_count": offered, "wrong_selected_count": wrong,
				"last_offered_at": lastOff.Int64, "last_selected_at": lastSel.Int64,
			}
		}
		return res, rows.Err()
	}

	confA, err := loadConf(versionA)
	if err != nil {
		return false, "", fmt.Errorf("load confusion A: %w", err)
	}
	confB, err := loadConf(versionB)
	if err != nil {
		return false, "", fmt.Errorf("load confusion B: %w", err)
	}
	if !reflect.DeepEqual(confA, confB) {
		return false, fmt.Sprintf("confusion mismatch: versionA has %d records, versionB has %d records", len(confA), len(confB)), nil
	}

	return true, "", nil
}
