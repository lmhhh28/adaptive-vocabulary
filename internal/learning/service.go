package learning

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"adaptive-vocabulary/internal/core"
)

type LearningService struct {
	db                       *sql.DB
	scheduler                *FSRSScheduler
	policy                   FSRSPolicyConfig
	observationPolicyVersion string
	projectionVersion        string
	isReplay                 bool
}

func NewLearningService(db *sql.DB, policy FSRSPolicyConfig, observationPolicyVersion, projectionVersion string) *LearningService {
	if observationPolicyVersion == "" {
		observationPolicyVersion = "observation-v2"
	}
	return &LearningService{
		db:                       db,
		scheduler:                NewFSRSScheduler(policy),
		policy:                   policy,
		observationPolicyVersion: observationPolicyVersion,
		projectionVersion:        projectionVersion,
		isReplay:                 false,
	}
}

// ApplyAttempt processes an attempt event, generates reviews/evidence/memory/confusion/correction projections in tc.
func (s *LearningService) ApplyAttempt(tc *core.TxnContext, attempt *AttemptEvent, options []InstanceOption) (*ReviewEvent, error) {
	if attempt.ID == "" {
		attempt.ID = core.NewUUID()
	}
	if attempt.CreatedAt == 0 {
		attempt.CreatedAt = core.NowMicro()
	}
	if attempt.ObservedAt == 0 {
		attempt.ObservedAt = attempt.CreatedAt
	}

	// 1. Allocate event sequence
	if attempt.EventSequence == 0 {
		res, err := tc.Exec(
			"INSERT INTO global_sequence (event_name, created_at) VALUES (?, ?)",
			"attempt_"+attempt.Source,
			attempt.ObservedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("allocate attempt sequence: %w", err)
		}
		seq, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("get attempt sequence id: %w", err)
		}
		attempt.EventSequence = seq
	}

	// 2. Check if disputed or invalidated via event_revision
	var latestRevKind string
	err := tc.QueryRow(
		"SELECT revision_kind FROM event_revision WHERE (event_id = ? OR event_id = ?) ORDER BY created_at DESC, id DESC LIMIT 1",
		attempt.ID,
		attempt.InstanceID,
	).Scan(&latestRevKind)
	isDisputedOrInvalidated := false
	if err == nil {
		isDisputedOrInvalidated = (latestRevKind == "disputed" || latestRevKind == "invalidate")
	} else if err != sql.ErrNoRows {
		return nil, fmt.Errorf("check event revision: %w", err)
	}

	// 3. Serialize interruption flags
	var interruptionJSON *string
	if len(attempt.InterruptionFlags) > 0 {
		b, _ := json.Marshal(attempt.InterruptionFlags)
		s := string(b)
		interruptionJSON = &s
	}

	isCorrectInt := 0
	if attempt.IsCorrect {
		isCorrectInt = 1
	}

	// 4. Insert attempt_event
	queryAttempt := `
		INSERT INTO attempt_event (
			id, instance_id, session_id, sense_id, task_family, source, purpose,
			response_history, selected_option_id, final_option_id, first_response_event_id,
			final_response_event_id, revision_count, client_active_duration_ms,
			interruption_flags, grading_version, is_correct, observed_at, event_sequence, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err = tc.Exec(
		queryAttempt,
		attempt.ID,
		attempt.InstanceID,
		attempt.SessionID,
		attempt.SenseID,
		attempt.TaskFamily,
		attempt.Source,
		attempt.Purpose,
		attempt.ResponseHistory,
		attempt.SelectedOptionID,
		attempt.FinalOptionID,
		attempt.FirstResponseEventID,
		attempt.FinalResponseEventID,
		attempt.RevisionCount,
		attempt.ClientActiveDurationMs,
		interruptionJSON,
		attempt.GradingVersion,
		isCorrectInt,
		attempt.ObservedAt,
		attempt.EventSequence,
		attempt.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert attempt_event: %w", err)
	}

	// 5. Classify eligibility
	decision := ClassifyEligibility(attempt, isDisputedOrInvalidated)

	// Resolve projection version for projections
	projVersion := s.projectionVersion
	if !s.isReplay {
		pc, err := core.GetProjectionControl(tc)
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("get projection control: %w", err)
		}
		if err == nil {
			if pc.Mode == "rebuilding" {
				return nil, core.ErrUnderMaintenance
			}
			if pc.ActiveVersion != "" {
				projVersion = pc.ActiveVersion
			}
		}
	}
	if projVersion == "" {
		projVersion = "v1"
	}

	// 6. FSRS Review & Memory Projection
	var reviewEvent *ReviewEvent
	if decision.FSRSReview {
		review, err := s.applyFSRS(tc, attempt, projVersion)
		if err != nil {
			return nil, fmt.Errorf("apply fsrs review: %w", err)
		}
		reviewEvent = review
	}

	// 7. Evidence Projection
	if err := s.updateEvidence(tc, attempt, decision, projVersion); err != nil {
		return nil, fmt.Errorf("update evidence projection: %w", err)
	}

	// 8. Confusion Projection
	if err := s.updateConfusion(tc, attempt, options, decision, projVersion); err != nil {
		return nil, fmt.Errorf("update confusion projection: %w", err)
	}

	// 9. Correction Item
	if err := s.updateCorrectionItem(tc, attempt); err != nil {
		return nil, fmt.Errorf("update correction item: %w", err)
	}

	return reviewEvent, nil
}

func (s *LearningService) applyFSRS(tc *core.TxnContext, attempt *AttemptEvent, projVersion string) (*ReviewEvent, error) {
	// Load existing memory projection
	var mem MemoryProjection
	var lastReviewAt sql.NullInt64
	queryMem := `
		SELECT fsrs_state, fsrs_step, stability, difficulty, due_at, last_review_at, reps, lapses, elapsed_days, scheduled_days
		FROM memory_projection
		WHERE projection_version = ? AND sense_id = ? AND task_family = ?
	`
	err := tc.QueryRow(queryMem, projVersion, attempt.SenseID, attempt.TaskFamily).Scan(
		&mem.FSRSState,
		&mem.FSRSStep,
		&mem.Stability,
		&mem.Difficulty,
		&mem.DueAt,
		&lastReviewAt,
		&mem.Reps,
		&mem.Lapses,
		&mem.ElapsedDays,
		&mem.ScheduledDays,
	)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("query memory projection: %w", err)
	}

	if lastReviewAt.Valid {
		v := lastReviewAt.Int64
		mem.LastReviewAt = &v
	}

	card := CardFromMemoryProjection(&mem)
	reviewTime := MicroToTime(attempt.ObservedAt)

	nextCard, elapsedDays, scheduledDays, rating, err := s.scheduler.StepReview(card, reviewTime, attempt.IsCorrect)
	if err != nil {
		return nil, fmt.Errorf("step review: %w", err)
	}

	reviewID := core.NewUUID()
	dueAt := TimeToMicro(nextCard.Due)

	review := &ReviewEvent{
		ID:                       reviewID,
		AttemptID:                attempt.ID,
		SenseID:                  attempt.SenseID,
		TaskFamily:               attempt.TaskFamily,
		ObservationPolicyVersion: s.observationPolicyVersion,
		Rating:                   rating,
		ReviewTime:               attempt.ObservedAt,
		FSRSState:                int(nextCard.State),
		FSRSStep:                 nextCard.RemainingSteps,
		Stability:                nextCard.Stability,
		Difficulty:               nextCard.Difficulty,
		DueAt:                    dueAt,
		Reps:                     int(nextCard.Reps),
		Lapses:                   int(nextCard.Lapses),
		ElapsedDays:              elapsedDays,
		ScheduledDays:            scheduledDays,
		FSRSVersion:              s.policy.AlgorithmVersion,
		CreatedAt:                attempt.CreatedAt,
	}

	queryReview := `
		INSERT INTO review_event (
			id, attempt_id, sense_id, task_family, observation_policy_version,
			rating, review_time, fsrs_state, fsrs_step, stability, difficulty,
			due_at, reps, lapses, elapsed_days, scheduled_days, fsrs_version, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (attempt_id, observation_policy_version) DO UPDATE SET
			rating = excluded.rating,
			review_time = excluded.review_time,
			fsrs_state = excluded.fsrs_state,
			fsrs_step = excluded.fsrs_step,
			stability = excluded.stability,
			difficulty = excluded.difficulty,
			due_at = excluded.due_at,
			reps = excluded.reps,
			lapses = excluded.lapses,
			elapsed_days = excluded.elapsed_days,
			scheduled_days = excluded.scheduled_days,
			fsrs_version = excluded.fsrs_version
	`
	_, err = tc.Exec(
		queryReview,
		review.ID,
		review.AttemptID,
		review.SenseID,
		review.TaskFamily,
		review.ObservationPolicyVersion,
		review.Rating,
		review.ReviewTime,
		review.FSRSState,
		review.FSRSStep,
		review.Stability,
		review.Difficulty,
		review.DueAt,
		review.Reps,
		review.Lapses,
		review.ElapsedDays,
		review.ScheduledDays,
		review.FSRSVersion,
		review.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert review_event: %w", err)
	}

	queryUpsertMem := `
		INSERT INTO memory_projection (
			projection_version, sense_id, task_family, fsrs_state, fsrs_step,
			stability, difficulty, due_at, last_review_at, reps, lapses,
			elapsed_days, scheduled_days, applied_event_sequence, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (projection_version, sense_id, task_family) DO UPDATE SET
			fsrs_state = excluded.fsrs_state,
			fsrs_step = excluded.fsrs_step,
			stability = excluded.stability,
			difficulty = excluded.difficulty,
			due_at = excluded.due_at,
			last_review_at = excluded.last_review_at,
			reps = excluded.reps,
			lapses = excluded.lapses,
			elapsed_days = excluded.elapsed_days,
			scheduled_days = excluded.scheduled_days,
			applied_event_sequence = excluded.applied_event_sequence,
			updated_at = excluded.updated_at
	`
	_, err = tc.Exec(
		queryUpsertMem,
		projVersion,
		attempt.SenseID,
		attempt.TaskFamily,
		int(nextCard.State),
		nextCard.RemainingSteps,
		nextCard.Stability,
		nextCard.Difficulty,
		dueAt,
		attempt.ObservedAt,
		int(nextCard.Reps),
		int(nextCard.Lapses),
		elapsedDays,
		scheduledDays,
		attempt.EventSequence,
		attempt.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upsert memory_projection: %w", err)
	}

	return review, nil
}

func (s *LearningService) updateEvidence(tc *core.TxnContext, attempt *AttemptEvent, decision EligibilityDecision, projVersion string) error {
	if !decision.IsValid {
		return nil
	}

	var ev EvidenceProjection
	var lastObservedAt, earliestObservedAt sql.NullInt64
	var lastCorrect sql.NullInt64

	queryEv := `
		SELECT ordinary_samples, ordinary_correct, normal_samples, normal_correct,
		       early_review_samples, early_review_correct, exam_single_samples, exam_single_correct,
		       correction_samples, correction_correct, exam_revised_samples,
		       exam_telemetry_incomplete_samples, omitted_samples, last_ordinary_observed_at,
		       last_ordinary_correct, earliest_ordinary_observed_at, needs_consolidation
		FROM evidence_projection
		WHERE projection_version = ? AND sense_id = ? AND task_family = ?
	`
	err := tc.QueryRow(queryEv, projVersion, attempt.SenseID, attempt.TaskFamily).Scan(
		&ev.OrdinarySamples,
		&ev.OrdinaryCorrect,
		&ev.NormalSamples,
		&ev.NormalCorrect,
		&ev.EarlyReviewSamples,
		&ev.EarlyReviewCorrect,
		&ev.ExamSingleSamples,
		&ev.ExamSingleCorrect,
		&ev.CorrectionSamples,
		&ev.CorrectionCorrect,
		&ev.ExamRevisedSamples,
		&ev.ExamTelemetryIncompleteSamples,
		&ev.OmittedSamples,
		&lastObservedAt,
		&lastCorrect,
		&earliestObservedAt,
		&ev.NeedsConsolidation,
	)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("query evidence_projection: %w", err)
	}

	if lastObservedAt.Valid {
		v := lastObservedAt.Int64
		ev.LastOrdinaryObservedAt = &v
	}
	if lastCorrect.Valid {
		b := lastCorrect.Int64 == 1
		ev.LastOrdinaryCorrect = &b
	}
	if earliestObservedAt.Valid {
		v := earliestObservedAt.Int64
		ev.EarliestOrdinaryObservedAt = &v
	}

	// Update counts
	if decision.OrdinaryEvidence {
		ev.OrdinarySamples++
		if attempt.IsCorrect {
			ev.OrdinaryCorrect++
		}
	}

	if attempt.Source == SourcePractice {
		switch attempt.Purpose {
		case PurposeNormal:
			ev.NormalSamples++
			if attempt.IsCorrect {
				ev.NormalCorrect++
			}
		case PurposeEarlyReview:
			ev.EarlyReviewSamples++
			if attempt.IsCorrect {
				ev.EarlyReviewCorrect++
			}
		case PurposeCorrection:
			ev.CorrectionSamples++
			if attempt.IsCorrect {
				ev.CorrectionCorrect++
			}
		}
	} else if attempt.Source == SourceExam {
		switch attempt.ResponseHistory {
		case ResponseSingleResponse:
			ev.ExamSingleSamples++
			if attempt.IsCorrect {
				ev.ExamSingleCorrect++
			}
		case ResponseRevised:
			ev.ExamRevisedSamples++
		case ResponseTelemetryIncomplete:
			ev.ExamTelemetryIncompleteSamples++
		case ResponseOmitted:
			ev.OmittedSamples++
		}
	}

	if decision.UpdateConsolidation {
		ev.LastOrdinaryObservedAt = &attempt.ObservedAt
		b := attempt.IsCorrect
		ev.LastOrdinaryCorrect = &b
		if ev.EarliestOrdinaryObservedAt == nil {
			ev.EarliestOrdinaryObservedAt = &attempt.ObservedAt
		}
		// R05: wrong sets needs_consolidation; correct clears it
		ev.NeedsConsolidation = !attempt.IsCorrect
	}

	needsConsolidationInt := 0
	if ev.NeedsConsolidation {
		needsConsolidationInt = 1
	}

	var lastCorrectVal any
	if ev.LastOrdinaryCorrect != nil {
		if *ev.LastOrdinaryCorrect {
			lastCorrectVal = 1
		} else {
			lastCorrectVal = 0
		}
	}

	queryUpsertEv := `
		INSERT INTO evidence_projection (
			projection_version, sense_id, task_family, ordinary_samples, ordinary_correct,
			normal_samples, normal_correct, early_review_samples, early_review_correct,
			exam_single_samples, exam_single_correct, correction_samples, correction_correct,
			exam_revised_samples, exam_telemetry_incomplete_samples, omitted_samples,
			last_ordinary_observed_at, last_ordinary_correct, earliest_ordinary_observed_at,
			needs_consolidation, applied_event_sequence, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (projection_version, sense_id, task_family) DO UPDATE SET
			ordinary_samples = excluded.ordinary_samples,
			ordinary_correct = excluded.ordinary_correct,
			normal_samples = excluded.normal_samples,
			normal_correct = excluded.normal_correct,
			early_review_samples = excluded.early_review_samples,
			early_review_correct = excluded.early_review_correct,
			exam_single_samples = excluded.exam_single_samples,
			exam_single_correct = excluded.exam_single_correct,
			correction_samples = excluded.correction_samples,
			correction_correct = excluded.correction_correct,
			exam_revised_samples = excluded.exam_revised_samples,
			exam_telemetry_incomplete_samples = excluded.exam_telemetry_incomplete_samples,
			omitted_samples = excluded.omitted_samples,
			last_ordinary_observed_at = excluded.last_ordinary_observed_at,
			last_ordinary_correct = excluded.last_ordinary_correct,
			earliest_ordinary_observed_at = excluded.earliest_ordinary_observed_at,
			needs_consolidation = excluded.needs_consolidation,
			applied_event_sequence = excluded.applied_event_sequence,
			updated_at = excluded.updated_at
	`
	_, err = tc.Exec(
		queryUpsertEv,
		projVersion,
		attempt.SenseID,
		attempt.TaskFamily,
		ev.OrdinarySamples,
		ev.OrdinaryCorrect,
		ev.NormalSamples,
		ev.NormalCorrect,
		ev.EarlyReviewSamples,
		ev.EarlyReviewCorrect,
		ev.ExamSingleSamples,
		ev.ExamSingleCorrect,
		ev.CorrectionSamples,
		ev.CorrectionCorrect,
		ev.ExamRevisedSamples,
		ev.ExamTelemetryIncompleteSamples,
		ev.OmittedSamples,
		ev.LastOrdinaryObservedAt,
		lastCorrectVal,
		ev.EarliestOrdinaryObservedAt,
		needsConsolidationInt,
		attempt.EventSequence,
		attempt.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert evidence_projection: %w", err)
	}

	return nil
}

func (s *LearningService) updateConfusion(tc *core.TxnContext, attempt *AttemptEvent, options []InstanceOption, decision EligibilityDecision, projVersion string) error {
	if !decision.IsValid {
		return nil
	}

	statGroup := ConfusionStatGroupUncertain
	if decision.OrdinaryConfusion {
		statGroup = ConfusionStatGroupOrdinary
	} else if attempt.Purpose == PurposeCorrection {
		statGroup = ConfusionStatGroupCorrection
	}

	for _, opt := range options {
		if opt.IsCorrect || opt.OptionIdentityID == "" {
			continue
		}

		isWrongSelected := false
		if !attempt.IsCorrect && attempt.SelectedOptionID != nil && *attempt.SelectedOptionID == opt.ID {
			isWrongSelected = true
		}

		wrongSelectedInc := 0
		var lastSelectedVal any
		if isWrongSelected {
			wrongSelectedInc = 1
			lastSelectedVal = attempt.ObservedAt
		}

		queryUpsertConf := `
			INSERT INTO confusion_projection (
				projection_version, target_sense_id, task_family, distractor_identity_id, stat_group,
				eligible_offered_count, wrong_selected_count, last_offered_at, last_selected_at,
				applied_event_sequence, updated_at
			) VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)
			ON CONFLICT (projection_version, target_sense_id, task_family, distractor_identity_id, stat_group) DO UPDATE SET
				eligible_offered_count = eligible_offered_count + 1,
				wrong_selected_count = wrong_selected_count + excluded.wrong_selected_count,
				last_offered_at = excluded.last_offered_at,
				last_selected_at = CASE WHEN excluded.wrong_selected_count > 0 THEN excluded.last_selected_at ELSE last_selected_at END,
				applied_event_sequence = excluded.applied_event_sequence,
				updated_at = excluded.updated_at
		`
		_, err := tc.Exec(
			queryUpsertConf,
			projVersion,
			attempt.SenseID,
			attempt.TaskFamily,
			opt.OptionIdentityID,
			statGroup,
			wrongSelectedInc,
			attempt.ObservedAt,
			lastSelectedVal,
			attempt.EventSequence,
			attempt.CreatedAt,
		)
		if err != nil {
			return fmt.Errorf("upsert confusion_projection: %w", err)
		}
	}

	return nil
}

func (s *LearningService) updateCorrectionItem(tc *core.TxnContext, attempt *AttemptEvent) error {
	if attempt.Source != SourcePractice {
		return nil
	}

	if attempt.Purpose == PurposeCorrection {
		var retries int
		err := tc.QueryRow(
			"SELECT retries_count FROM correction_item WHERE session_id = ? AND sense_id = ? AND task_family = ?",
			attempt.SessionID,
			attempt.SenseID,
			attempt.TaskFamily,
		).Scan(&retries)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("query correction_item: %w", err)
		}

		retries++
		newStatus := CorrectionStatusPending
		if attempt.IsCorrect {
			newStatus = CorrectionStatusCompleted
		} else if retries >= 2 {
			newStatus = CorrectionStatusExhausted
		}

		queryUpdate := `
			UPDATE correction_item
			SET retries_count = ?, status = ?, triggering_attempt_id = ?, triggering_event_sequence = ?, updated_at = ?
			WHERE session_id = ? AND sense_id = ? AND task_family = ?
		`
		_, err = tc.Exec(queryUpdate, retries, newStatus, attempt.ID, attempt.EventSequence, attempt.CreatedAt, attempt.SessionID, attempt.SenseID, attempt.TaskFamily)
		if err != nil {
			return fmt.Errorf("update correction_item: %w", err)
		}
	} else if (attempt.Purpose == PurposeNormal || attempt.Purpose == PurposeEarlyReview) && !attempt.IsCorrect {
		queryUpsertCorr := `
			INSERT INTO correction_item (
				session_id, sense_id, task_family, triggering_attempt_id, triggering_event_sequence,
				retries_count, status, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, 0, 'pending', ?, ?)
			ON CONFLICT (session_id, sense_id, task_family) DO UPDATE SET
				triggering_attempt_id = excluded.triggering_attempt_id,
				triggering_event_sequence = excluded.triggering_event_sequence,
				retries_count = 0,
				status = 'pending',
				updated_at = excluded.updated_at
		`
		_, err := tc.Exec(
			queryUpsertCorr,
			attempt.SessionID,
			attempt.SenseID,
			attempt.TaskFamily,
			attempt.ID,
			attempt.EventSequence,
			attempt.CreatedAt,
			attempt.CreatedAt,
		)
		if err != nil {
			return fmt.Errorf("upsert correction_item on normal error: %w", err)
		}
	}

	return nil
}
