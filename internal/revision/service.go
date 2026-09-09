package revision

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"adaptive-vocabulary/internal/core"
	"adaptive-vocabulary/internal/maintenance"
)

var (
	ErrSessionActive     = errors.New("cannot perform operation while a session is active")
	ErrRevisionConflict  = errors.New("revision conflict on question version")
	ErrVersionNotFound   = errors.New("question version not found")
	ErrInvalidAction     = errors.New("invalid disposition action")
)

type Service struct {
	db             *sql.DB
	maintService   *maintenance.Service
}

func NewService(db *sql.DB, maintService *maintenance.Service) *Service {
	return &Service{
		db:           db,
		maintService: maintService,
	}
}

func (s *Service) DB() *sql.DB {
	return s.db
}

// ApplyEvidenceDisposition handles disputed, invalidate, or restore_validity.
// Rejects with ErrSessionActive if an active session exists.
func (s *Service) ApplyEvidenceDisposition(
	ctx context.Context,
	versionID string,
	action string,
	reason string,
	expectedRevision *int64,
) (*EvidenceDispositionResponse, error) {
	if action != "disputed" && action != "invalidate" && action != "restore_validity" {
		return nil, fmt.Errorf("%w: %s", ErrInvalidAction, action)
	}

	// 1. Check for active sessions OUTSIDE transaction first
	var openCount int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM session WHERE status = 'open'").Scan(&openCount)
	if err != nil {
		return nil, fmt.Errorf("check active sessions: %w", err)
	}
	if openCount > 0 {
		return nil, ErrSessionActive
	}

	var dispID string
	var run *maintenance.ModelRebuildRun
	now := time.Now().UTC().UnixNano() / 1000

	err = core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		// Re-check for active sessions INSIDE write transaction
		var openInTx int
		err := tc.QueryRow("SELECT count(*) FROM session WHERE status = 'open'").Scan(&openInTx)
		if err != nil {
			return err
		}
		if openInTx > 0 {
			return ErrSessionActive
		}

		// Check question_version
		var currentRev int64
		var currentStatus string
		err = tc.QueryRow("SELECT status, revision FROM question_version WHERE id = ?", versionID).Scan(&currentStatus, &currentRev)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrVersionNotFound
			}
			return fmt.Errorf("query question_version: %w", err)
		}

		if expectedRevision != nil && *expectedRevision != currentRev {
			return ErrRevisionConflict
		}

		// Determine target question_version status
		var targetStatus string
		switch action {
		case "disputed":
			targetStatus = "disputed"
		case "invalidate":
			targetStatus = "invalidated"
		case "restore_validity":
			targetStatus = "published"
		}

		// Update question_version
		newRev := currentRev + 1
		_, err = tc.Exec(
			"UPDATE question_version SET status = ?, revision = ?, updated_at = ? WHERE id = ?",
			targetStatus, newRev, now, versionID,
		)
		if err != nil {
			return fmt.Errorf("update question_version status: %w", err)
		}

		// Find affected instances and attempts
		type instAttempt struct {
			instanceID string
			attemptID  sql.NullString
			sessionID  string
		}
		queryInstances := `
			SELECT qi.id, ae.id, qi.session_id
			FROM question_instance qi
			LEFT JOIN attempt_event ae ON ae.instance_id = qi.id
			WHERE qi.question_version_id = ?
		`
		rows, err := tc.Query(queryInstances, versionID)
		if err != nil {
			return fmt.Errorf("query affected instances: %w", err)
		}
		defer rows.Close()

		var affected []instAttempt
		affectedSessions := make(map[string]bool)
		for rows.Next() {
			var ia instAttempt
			if err := rows.Scan(&ia.instanceID, &ia.attemptID, &ia.sessionID); err != nil {
				return err
			}
			affected = append(affected, ia)
			affectedSessions[ia.sessionID] = true
		}
		if err := rows.Err(); err != nil {
			return err
		}

		// Append event_revision records
		for _, item := range affected {
			instRevID := core.NewUUID()
			_, err = tc.Exec(
				"INSERT INTO event_revision (id, event_id, event_type, revision_kind, reason, created_at) VALUES (?, ?, 'question_instance', ?, ?, ?)",
				instRevID, item.instanceID, action, reason, now,
			)
			if err != nil {
				return fmt.Errorf("insert instance event_revision: %w", err)
			}

			if item.attemptID.Valid && item.attemptID.String != "" {
				attRevID := core.NewUUID()
				_, err = tc.Exec(
					"INSERT INTO event_revision (id, event_id, event_type, revision_kind, reason, created_at) VALUES (?, ?, 'attempt_event', ?, ?, ?)",
					attRevID, item.attemptID.String, action, reason, now,
				)
				if err != nil {
					return fmt.Errorf("insert attempt event_revision: %w", err)
				}
			}
		}

		// Recompute exam reports if action is invalidate or restore_validity
		if action == "invalidate" || action == "restore_validity" {
			for sessID := range affectedSessions {
				var assessID string
				var curResultRev int64
				err := tc.QueryRow("SELECT id, result_revision FROM assessment_result WHERE session_id = ?", sessID).Scan(&assessID, &curResultRev)
				if errors.Is(err, sql.ErrNoRows) {
					continue // Not an exam or not submitted yet
				} else if err != nil {
					return fmt.Errorf("query assessment_result for session %s: %w", sessID, err)
				}

				// Query all items for this assessment to calculate void, correct, total
				queryItems := `
					SELECT air.instance_id, air.is_correct, air.selected_option_id, qv.status
					FROM assessment_item_result air
					JOIN question_instance qi ON qi.id = air.instance_id
					JOIN question_version qv ON qv.id = qi.question_version_id
					WHERE air.assessment_id = ?
				`
				itemRows, err := tc.Query(queryItems, assessID)
				if err != nil {
					return fmt.Errorf("query assessment items: %w", err)
				}

				total := 0
				voidCount := 0
				correctCount := 0
				omittedCount := 0
				var voidInstances []string

				for itemRows.Next() {
					var instID string
					var isCorr int
					var selOpt sql.NullString
					var qvStatus string
					if err := itemRows.Scan(&instID, &isCorr, &selOpt, &qvStatus); err != nil {
						itemRows.Close()
						return err
					}
					total++
					if qvStatus == "invalidated" {
						voidCount++
						voidInstances = append(voidInstances, instID)
					} else {
						if isCorr == 1 {
							correctCount++
						}
						if !selOpt.Valid || selOpt.String == "" {
							omittedCount++
						}
					}
				}
				itemRows.Close()

				validCount := total - voidCount
				var ratioPtr *float64
				if validCount > 0 {
					r := float64(correctCount) / float64(validCount)
					ratioPtr = &r
				} // If validCount == 0, ratioPtr remains nil (null)

				newResultRev := curResultRev + 1
				_, err = tc.Exec(
					"UPDATE assessment_result SET void_count = ?, correct_count = ?, omitted_count = ?, ratio = ?, result_revision = ?, updated_at = ? WHERE id = ?",
					voidCount, correctCount, omittedCount, ratioPtr, newResultRev, now, assessID,
				)
				if err != nil {
					return fmt.Errorf("update assessment_result: %w", err)
				}

				voidJSON, _ := json.Marshal(voidInstances)
				revID := core.NewUUID()
				insertRev := `
					INSERT INTO assessment_result_revision (
						id, assessment_id, session_id, result_revision, total_count,
						correct_count, omitted_count, void_count, ratio, void_instances_json,
						reason, created_at
					) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				`
				_, err = tc.Exec(insertRev,
					revID, assessID, sessID, newResultRev, total,
					correctCount, omittedCount, voidCount, ratioPtr, string(voidJSON),
					reason, now,
				)
				if err != nil {
					return fmt.Errorf("insert assessment_result_revision: %w", err)
				}
			}
		}

		// Initialize rebuild run
		pc, err := core.GetProjectionControl(tc)
		if err != nil {
			return fmt.Errorf("get projection control: %w", err)
		}

		digest, err := s.maintService.ComputeRevisionDigest(ctx, tc)
		if err != nil {
			return fmt.Errorf("compute revision digest: %w", err)
		}

		var watermark int64
		err = tc.QueryRow("SELECT coalesce(max(seq), 0) FROM global_sequence").Scan(&watermark)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("get watermark: %w", err)
		}

		runID := core.NewUUID()
		targetVersion := "v_" + runID[:8]
		run = &maintenance.ModelRebuildRun{
			ID:                      runID,
			Kind:                    "evidence_revision",
			BaseActiveVersion:       pc.ActiveVersion,
			TargetProjectionVersion: targetVersion,
			TargetPolicy:            "policy-v2",
			BaseRevisionDigest:      digest,
			FixedWatermark:          watermark,
			Status:                  "running",
			RequiresRebuild:         true,
			CreatedAt:               now,
			UpdatedAt:               now,
		}

		insertRun := `
			INSERT INTO model_rebuild_run (
				id, kind, base_active_version, target_projection_version, target_policy,
				base_revision_digest, fixed_watermark, status, requires_rebuild,
				created_at, updated_at
			) VALUES (?, 'evidence_revision', ?, ?, 'policy-v2', ?, ?, 'running', 1, ?, ?)
		`
		_, err = tc.Exec(insertRun, run.ID, run.BaseActiveVersion, run.TargetProjectionVersion, run.BaseRevisionDigest, run.FixedWatermark, now, now)
		if err != nil {
			return fmt.Errorf("insert model_rebuild_run: %w", err)
		}

		_, err = tc.Exec(
			"UPDATE projection_control SET mode = 'rebuilding', run_id = ?, run_status = 'running', requires_rebuild = 1, fixed_watermark = ?, updated_at = ? WHERE id = 1",
			run.ID, watermark, now,
		)
		if err != nil {
			return fmt.Errorf("update projection_control: %w", err)
		}

		// Record in evidence_disposition table
		dispID = core.NewUUID()
		_, err = tc.Exec(
			"INSERT INTO evidence_disposition (id, question_version_id, action, reason, rebuild_run_id, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			dispID, versionID, action, reason, run.ID, now,
		)
		if err != nil {
			return fmt.Errorf("insert evidence_disposition: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// 2. Perform Rebuild synchronously
	replayErr := s.maintService.ExecuteReplay(ctx, run)
	status := run.Status
	if replayErr != nil {
		status = "failed"
	}

	return &EvidenceDispositionResponse{
		DispositionID: dispID,
		RebuildRunID:  run.ID,
		Status:        status,
	}, nil
}

// RetireQuestionVersion marks a version as retired.
func (s *Service) RetireQuestionVersion(ctx context.Context, versionID, reason string, expectedRevision *int64) (*RetireResponse, error) {
	var newRev int64
	now := time.Now().UTC().UnixNano() / 1000

	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		var curRev int64
		err := tc.QueryRow("SELECT revision FROM question_version WHERE id = ?", versionID).Scan(&curRev)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrVersionNotFound
			}
			return fmt.Errorf("query question_version: %w", err)
		}
		if expectedRevision != nil && *expectedRevision != curRev {
			return ErrRevisionConflict
		}

		newRev = curRev + 1
		_, err = tc.Exec("UPDATE question_version SET status = 'retired', revision = ?, updated_at = ? WHERE id = ?", newRev, now, versionID)
		if err != nil {
			return fmt.Errorf("update question_version to retired: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &RetireResponse{
		VersionID: versionID,
		Status:    "retired",
		Revision:  newRev,
	}, nil
}

// GetLearningSenseDetail loads detailed learning state for a sense across tasks.
// Fails with ErrSessionActive if an active session exists.
func (s *Service) GetLearningSenseDetail(ctx context.Context, senseID string) (*SenseLearningDetail, error) {
	var openCount int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM session WHERE status = 'open'").Scan(&openCount)
	if err != nil {
		return nil, err
	}
	if openCount > 0 {
		return nil, ErrSessionActive
	}

	pc, err := core.GetProjectionControl(s.db)
	if err != nil {
		return nil, err
	}

	taskFamilies := []string{"meaning_recognition", "reverse_selection", "context_selection"}
	var tasks []TaskLearningDetail

	for _, tf := range taskFamilies {
		// Evidence
		queryEv := `
			SELECT ordinary_samples, ordinary_correct, normal_samples, normal_correct,
			       early_review_samples, early_review_correct, exam_single_samples, exam_single_correct,
			       correction_samples, correction_correct, exam_revised_samples,
			       exam_telemetry_incomplete_samples, omitted_samples, last_ordinary_observed_at,
			       last_ordinary_correct, earliest_ordinary_observed_at, needs_consolidation
			FROM evidence_projection
			WHERE projection_version = ? AND sense_id = ? AND task_family = ?
		`
		var ordS, ordC, normS, normC, earlyS, earlyC, examS, examC, corrS, corrC, revS, uncS, omitS, needsC int
		var lastObs, earObs, lastCorr sql.NullInt64
		evMap := make(map[string]any)
		err := s.db.QueryRowContext(ctx, queryEv, pc.ActiveVersion, senseID, tf).Scan(
			&ordS, &ordC, &normS, &normC, &earlyS, &earlyC, &examS, &examC, &corrS, &corrC,
			&revS, &uncS, &omitS, &lastObs, &lastCorr, &earObs, &needsC,
		)
		if err == nil {
			evMap["ordinary_samples"] = ordS
			evMap["ordinary_correct"] = ordC
			evMap["normal_samples"] = normS
			evMap["normal_correct"] = normC
			evMap["early_review_samples"] = earlyS
			evMap["early_review_correct"] = earlyC
			evMap["exam_single_samples"] = examS
			evMap["exam_single_correct"] = examC
			evMap["correction_samples"] = corrS
			evMap["correction_correct"] = corrC
			evMap["exam_revised_samples"] = revS
			evMap["exam_telemetry_incomplete_samples"] = uncS
			evMap["omitted_samples"] = omitS
			evMap["needs_consolidation"] = (needsC == 1)
			if lastObs.Valid {
				evMap["last_ordinary_observed_at"] = lastObs.Int64
			}
			if lastCorr.Valid {
				evMap["last_ordinary_correct"] = lastCorr.Int64
			}
		}

		// Memory
		queryMem := `
			SELECT fsrs_state, fsrs_step, stability, difficulty, due_at, last_review_at, reps, lapses
			FROM memory_projection
			WHERE projection_version = ? AND sense_id = ? AND task_family = ?
		`
		var fState, fStep, reps, lapses int
		var stab, diff float64
		var dueAt int64
		var lastRev sql.NullInt64
		memMap := make(map[string]any)
		err = s.db.QueryRowContext(ctx, queryMem, pc.ActiveVersion, senseID, tf).Scan(
			&fState, &fStep, &stab, &diff, &dueAt, &lastRev, &reps, &lapses,
		)
		if err == nil {
			memMap["fsrs_state"] = fState
			memMap["fsrs_step"] = fStep
			memMap["stability"] = stab
			memMap["difficulty"] = diff
			memMap["due_at"] = dueAt
			memMap["reps"] = reps
			memMap["lapses"] = lapses
			if lastRev.Valid {
				memMap["last_review_at"] = lastRev.Int64
			}
		}

		// Confusion
		queryConf := `
			SELECT distractor_identity_id, stat_group, eligible_offered_count, wrong_selected_count
			FROM confusion_projection
			WHERE projection_version = ? AND target_sense_id = ? AND task_family = ?
		`
		confRows, err := s.db.QueryContext(ctx, queryConf, pc.ActiveVersion, senseID, tf)
		var confList []map[string]any
		if err == nil {
			for confRows.Next() {
				var distID, sGroup string
				var offered, wrong int
				if err := confRows.Scan(&distID, &sGroup, &offered, &wrong); err == nil {
					confList = append(confList, map[string]any{
						"distractor_identity_id": distID,
						"stat_group":             sGroup,
						"eligible_offered_count": offered,
						"wrong_selected_count":   wrong,
					})
				}
			}
			confRows.Close()
		}

		tasks = append(tasks, TaskLearningDetail{
			TaskFamily: tf,
			Evidence:   evMap,
			Memory:     memMap,
			Confusions: confList,
		})
	}

	return &SenseLearningDetail{
		SenseID:           senseID,
		ProjectionVersion: pc.ActiveVersion,
		Tasks:             tasks,
	}, nil
}

// GetLearningSummary loads high-level counts by task without leaking secrets.
func (s *Service) GetLearningSummary(ctx context.Context) (*LearningSummaryResponse, error) {
	pc, err := core.GetProjectionControl(s.db)
	if err != nil {
		return nil, err
	}

	counts := make(map[string]int)
	query := `
		SELECT task_family, count(*)
		FROM evidence_projection
		WHERE projection_version = ?
		GROUP BY task_family
	`
	rows, err := s.db.QueryContext(ctx, query, pc.ActiveVersion)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var tf string
			var c int
			if err := rows.Scan(&tf, &c); err == nil {
				counts[tf] = c
			}
		}
	}

	evidenceConds := map[string]any{
		"ordinary_evidence_policy": "observation-v2",
		"active_version":           pc.ActiveVersion,
		"mode":                     pc.Mode,
	}

	return &LearningSummaryResponse{
		CountsByTask:       counts,
		EvidenceConditions: evidenceConds,
		ProjectionVersion:  pc.ActiveVersion,
	}, nil
}

// LoadQualityPageData aggregates data for the /quality web UI.
func (s *Service) LoadQualityPageData(ctx context.Context) (*QualityPageData, error) {
	maintState, err := s.maintService.GetMaintenanceState(ctx)
	if err != nil {
		return nil, err
	}

	// Dispositions
	queryDisp := `
		SELECT ed.id, ed.question_version_id, ed.action, ed.reason, coalesce(ed.rebuild_run_id, ''), ed.created_at,
		       qv.stem, qv.task_family
		FROM evidence_disposition ed
		JOIN question_version qv ON qv.id = ed.question_version_id
		ORDER BY ed.created_at DESC
		LIMIT 50
	`
	rows, err := s.db.QueryContext(ctx, queryDisp)
	var disps []DispositionRecord
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var d DispositionRecord
			if err := rows.Scan(&d.ID, &d.QuestionVersionID, &d.Action, &d.Reason, &d.RebuildRunID, &d.CreatedAt, &d.Stem, &d.TaskFamily); err == nil {
				disps = append(disps, d)
			}
		}
	}

	// Rebuild runs
	queryRuns := `
		SELECT id, kind, base_active_version, target_projection_version, target_policy,
		       base_revision_digest, fixed_watermark, status, requires_rebuild,
		       predecessor_run_id, error_message, created_at, updated_at
		FROM model_rebuild_run
		ORDER BY created_at DESC
		LIMIT 20
	`
	rRows, err := s.db.QueryContext(ctx, queryRuns)
	var runs []maintenance.ModelRebuildRun
	if err == nil {
		defer rRows.Close()
		for rRows.Next() {
			var r maintenance.ModelRebuildRun
			var predID, eMsg sql.NullString
			var reqReb int
			if err := rRows.Scan(
				&r.ID, &r.Kind, &r.BaseActiveVersion, &r.TargetProjectionVersion, &r.TargetPolicy,
				&r.BaseRevisionDigest, &r.FixedWatermark, &r.Status, &reqReb,
				&predID, &eMsg, &r.CreatedAt, &r.UpdatedAt,
			); err == nil {
				r.RequiresRebuild = (reqReb == 1)
				if predID.Valid {
					r.PredecessorRunID = &predID.String
				}
				if eMsg.Valid {
					r.ErrorMessage = &eMsg.String
				}
				runs = append(runs, r)
			}
		}
	}

	// Revised reports
	queryReports := `
		SELECT assessment_id, session_id, result_revision, total_count, correct_count,
		       omitted_count, void_count, ratio, reason, created_at
		FROM assessment_result_revision
		ORDER BY created_at DESC
		LIMIT 50
	`
	repRows, err := s.db.QueryContext(ctx, queryReports)
	var reports []RevisedReportItem
	if err == nil {
		defer repRows.Close()
		for repRows.Next() {
			var rep RevisedReportItem
			var ratVal sql.NullFloat64
			if err := repRows.Scan(
				&rep.AssessmentID, &rep.SessionID, &rep.ResultRevision, &rep.TotalCount,
				&rep.CorrectCount, &rep.OmittedCount, &rep.VoidCount, &ratVal,
				&rep.Reason, &rep.CreatedAt,
			); err == nil {
				if ratVal.Valid {
					rep.Ratio = &ratVal.Float64
				}
				reports = append(reports, rep)
			}
		}
	}

	// Question versions
	queryQ := `
		SELECT id, stem, task_family, status, revision
		FROM question_version
		ORDER BY created_at ASC
		LIMIT 100
	`
	qRows, err := s.db.QueryContext(ctx, queryQ)
	var questions []QuestionSummary
	if err == nil {
		defer qRows.Close()
		for qRows.Next() {
			var q QuestionSummary
			if err := qRows.Scan(&q.ID, &q.Stem, &q.TaskFamily, &q.Status, &q.Revision); err == nil {
				questions = append(questions, q)
			}
		}
	}

	return &QualityPageData{
		Maintenance:    maintState,
		Dispositions:   disps,
		RebuildRuns:    runs,
		RevisedReports: reports,
		Questions:      questions,
	}, nil
}
