package selection

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"adaptive-vocabulary/internal/content"
	"adaptive-vocabulary/internal/learning"
)

type Selector struct {
	db  *sql.DB
	cfg SelectionConfig
}

func NewSelector(db *sql.DB, cfg SelectionConfig) *Selector {
	return &Selector{
		db:  db,
		cfg: cfg,
	}
}

type candidateUnit struct {
	senseID                    string
	lexemeID                   string
	taskFamily                 string
	ordinarySamples            int
	dueAt                      int64
	earliestOrdinaryObservedAt int64
	needsConsolidation         bool
	otherTestedTasksCount      int
}

// SelectNextQuestion implements the frozen selection policy (selection-v2).
func (s *Selector) SelectNextQuestion(ctx context.Context, req SelectionRequest) (*SelectionResult, error) {
	if req.ProjectionVersion == "" {
		req.ProjectionVersion = "v1"
	}
	if req.NormalPosition <= 0 {
		req.NormalPosition = 1
	}

	// -------------------------------------------------------------
	// 1. Check Pending Corrections
	// -------------------------------------------------------------
	queryCorrections := `
		SELECT c.sense_id, c.task_family, c.triggering_event_sequence, s.lexeme_id
		FROM correction_item c
		JOIN sense s ON s.id = c.sense_id
		WHERE c.session_id = ? AND c.status = 'pending' AND c.retries_count < ?
		ORDER BY c.triggering_event_sequence ASC, c.sense_id ASC, c.task_family ASC
	`
	rowsCorr, err := s.db.QueryContext(ctx, queryCorrections, req.SessionID, s.cfg.MaxCorrectionsPerUnitPerSession)
	if err != nil {
		return nil, fmt.Errorf("query pending corrections: %w", err)
	}
	type rawCorrection struct {
		senseID       string
		taskFamily    string
		lexemeID      string
		triggeringSeq int64
	}
	var rawCorrections []rawCorrection
	for rowsCorr.Next() {
		var rc rawCorrection
		if err := rowsCorr.Scan(&rc.senseID, &rc.taskFamily, &rc.triggeringSeq, &rc.lexemeID); err != nil {
			_ = rowsCorr.Close()
			return nil, fmt.Errorf("scan correction item: %w", err)
		}
		rawCorrections = append(rawCorrections, rc)
	}
	if err := rowsCorr.Err(); err != nil {
		_ = rowsCorr.Close()
		return nil, fmt.Errorf("iterate corrections: %w", err)
	}
	_ = rowsCorr.Close()

	type eligibleCorrection struct {
		senseID       string
		taskFamily    string
		lexemeID      string
		triggeringSeq int64
		qv            *content.QuestionVersion
		formID        string
	}
	var eligibleCorrections []eligibleCorrection

	for _, rc := range rawCorrections {
		senseID := rc.senseID
		taskFamily := rc.taskFamily
		lexemeID := rc.lexemeID
		triggeringSeq := rc.triggeringSeq

		// Check cooling eligibility (3 distinct lexemes)
		isCoolingEligible, _, err := CheckCorrectionEligibility(ctx, s.db, s.cfg, req.SessionID, senseID, taskFamily, lexemeID)
		if err != nil {
			return nil, fmt.Errorf("check correction cooling: %w", err)
		}
		if !isCoolingEligible {
			continue
		}

		// Query published question versions for this unit
		forms, err := s.getPublishedVersionsForUnit(ctx, senseID, taskFamily)
		if err != nil {
			return nil, fmt.Errorf("get published versions: %w", err)
		}

		// Correction requires a new form not answered in this session for this unit
		answeredForms, err := s.getAnsweredFormsInSession(ctx, req.SessionID, senseID, taskFamily)
		if err != nil {
			return nil, fmt.Errorf("get answered forms: %w", err)
		}

		var availableForms []*content.QuestionVersion
		for _, f := range forms {
			if !answeredForms[f.FormID] {
				availableForms = append(availableForms, f)
			}
		}

		if len(availableForms) == 0 {
			// No new forms available -> blocked_content
			continue
		}

		// Pick best form among available
		chosenQV, err := s.pickBestForm(ctx, req.SessionID, req.ProjectionVersion, senseID, taskFamily, availableForms)
		if err != nil {
			return nil, fmt.Errorf("pick best form for correction: %w", err)
		}

		eligibleCorrections = append(eligibleCorrections, eligibleCorrection{
			senseID:       senseID,
			taskFamily:    taskFamily,
			lexemeID:      lexemeID,
			triggeringSeq: triggeringSeq,
			qv:            chosenQV,
			formID:        chosenQV.FormID,
		})
	}

	if len(eligibleCorrections) > 0 {
		topCorr := eligibleCorrections[0]
		return &SelectionResult{
			Status:          "ready",
			CandidateGroup:  "correction",
			IsCorrection:    true,
			QuestionVersion: topCorr.qv,
			FormID:          topCorr.formID,
			SenseID:         topCorr.senseID,
			LexemeID:        topCorr.lexemeID,
			TaskFamily:      topCorr.taskFamily,
			SelectionReason: fmt.Sprintf("correction retry for triggering_sequence=%d", topCorr.triggeringSeq),
		}, nil
	}

	// -------------------------------------------------------------
	// 2. Normal Slot Selection
	// -------------------------------------------------------------
	queryBankSenses := `
		SELECT bs.sense_id, s.lexeme_id
		FROM bank_sense bs
		JOIN sense s ON s.id = bs.sense_id
		WHERE bs.bank_id = ?
		ORDER BY bs.sense_id ASC
	`
	rowsBank, err := s.db.QueryContext(ctx, queryBankSenses, req.BankID)
	if err != nil {
		return nil, fmt.Errorf("query bank senses: %w", err)
	}
	defer rowsBank.Close()

	type bankSense struct {
		senseID  string
		lexemeID string
	}
	var bankSenses []bankSense
	senseToLexeme := make(map[string]string)
	for rowsBank.Next() {
		var bs bankSense
		if err := rowsBank.Scan(&bs.senseID, &bs.lexemeID); err != nil {
			return nil, fmt.Errorf("scan bank sense: %w", err)
		}
		bankSenses = append(bankSenses, bs)
		senseToLexeme[bs.senseID] = bs.lexemeID
	}
	if err := rowsBank.Err(); err != nil {
		return nil, fmt.Errorf("iterate bank senses: %w", err)
	}

	// 2a. Batch query published question units in this bank
	queryPublished := `
		SELECT DISTINCT qv.sense_id, qv.task_family
		FROM bank_sense bs
		JOIN question_version qv ON qv.sense_id = bs.sense_id
		WHERE bs.bank_id = ? AND qv.status = 'published'
	`
	rowsPub, err := s.db.QueryContext(ctx, queryPublished, req.BankID)
	if err != nil {
		return nil, fmt.Errorf("query published question units: %w", err)
	}
	defer rowsPub.Close()

	hasPublished := make(map[string]bool)
	for rowsPub.Next() {
		var sID, tf string
		if err := rowsPub.Scan(&sID, &tf); err != nil {
			return nil, fmt.Errorf("scan published unit: %w", err)
		}
		hasPublished[sID+":"+tf] = true
	}
	if err := rowsPub.Err(); err != nil {
		return nil, fmt.Errorf("iterate published units: %w", err)
	}

	// 2b. Batch query evidence projections in this bank for the active projection version
	type evData struct {
		ordinarySamples            int
		earliestOrdinaryObservedAt int64
		needsConsolidation         bool
	}
	evMap := make(map[string]evData)
	lexemeTestedTasks := make(map[string]map[string]bool)

	queryEvBatch := `
		SELECT ep.sense_id, ep.task_family, ep.ordinary_samples, ep.earliest_ordinary_observed_at, ep.needs_consolidation
		FROM bank_sense bs
		JOIN evidence_projection ep ON ep.sense_id = bs.sense_id
		WHERE bs.bank_id = ? AND ep.projection_version = ?
	`
	rowsEv, err := s.db.QueryContext(ctx, queryEvBatch, req.BankID, req.ProjectionVersion)
	if err != nil {
		return nil, fmt.Errorf("query evidence projections: %w", err)
	}
	defer rowsEv.Close()

	for rowsEv.Next() {
		var sID, tf string
		var ordS int
		var earObs sql.NullInt64
		var needsC int
		if err := rowsEv.Scan(&sID, &tf, &ordS, &earObs, &needsC); err != nil {
			return nil, fmt.Errorf("scan evidence projection: %w", err)
		}
		evMap[sID+":"+tf] = evData{
			ordinarySamples:            ordS,
			earliestOrdinaryObservedAt: earObs.Int64,
			needsConsolidation:         needsC == 1,
		}
		if ordS > 0 {
			if lexemeID, ok := senseToLexeme[sID]; ok {
				if lexemeTestedTasks[lexemeID] == nil {
					lexemeTestedTasks[lexemeID] = make(map[string]bool)
				}
				lexemeTestedTasks[lexemeID][tf] = true
			}
		}
	}
	if err := rowsEv.Err(); err != nil {
		return nil, fmt.Errorf("iterate evidence projections: %w", err)
	}

	// 2c. Batch query memory projections in this bank for the active projection version
	memMap := make(map[string]int64)
	queryMemBatch := `
		SELECT mp.sense_id, mp.task_family, mp.due_at
		FROM bank_sense bs
		JOIN memory_projection mp ON mp.sense_id = bs.sense_id
		WHERE bs.bank_id = ? AND mp.projection_version = ?
	`
	rowsMem, err := s.db.QueryContext(ctx, queryMemBatch, req.BankID, req.ProjectionVersion)
	if err != nil {
		return nil, fmt.Errorf("query memory projections: %w", err)
	}
	defer rowsMem.Close()

	for rowsMem.Next() {
		var sID, tf string
		var dueAt int64
		if err := rowsMem.Scan(&sID, &tf, &dueAt); err != nil {
			return nil, fmt.Errorf("scan memory projection: %w", err)
		}
		memMap[sID+":"+tf] = dueAt
	}
	if err := rowsMem.Err(); err != nil {
		return nil, fmt.Errorf("iterate memory projections: %w", err)
	}

	// 2d. Cooldown caching per lexeme
	lexemeCooldown := make(map[string]bool)

	var cooldownCount int
	var contentMissingCount int
	var exhaustedCount int

	var dueCandidates []candidateUnit
	var unseenCandidates []candidateUnit
	var earlyCandidates []candidateUnit

	for _, bs := range bankSenses {
		for _, tf := range learning.TaskFamilies {
			key := bs.senseID + ":" + tf
			if !hasPublished[key] {
				contentMissingCount++
				continue
			}

			// Check normal cooling for lexeme with per-request cache
			inCooldown, ok := lexemeCooldown[bs.lexemeID]
			if !ok {
				var err error
				inCooldown, err = CheckNormalCooldown(ctx, s.db, s.cfg, req.SessionID, bs.lexemeID, req.NowMicro)
				if err != nil {
					return nil, fmt.Errorf("check normal cooldown: %w", err)
				}
				lexemeCooldown[bs.lexemeID] = inCooldown
			}
			if inCooldown {
				cooldownCount++
				continue
			}

			ev := evMap[key]
			dueAt := memMap[key]

			cand := candidateUnit{
				senseID:                    bs.senseID,
				lexemeID:                   bs.lexemeID,
				taskFamily:                 tf,
				ordinarySamples:            ev.ordinarySamples,
				dueAt:                      dueAt,
				earliestOrdinaryObservedAt: ev.earliestOrdinaryObservedAt,
				needsConsolidation:         ev.needsConsolidation,
			}

			if ev.ordinarySamples == 0 {
				cand.otherTestedTasksCount = len(lexemeTestedTasks[bs.lexemeID])
				unseenCandidates = append(unseenCandidates, cand)
			} else if req.NowMicro >= dueAt {
				dueCandidates = append(dueCandidates, cand)
			} else {
				earlyCandidates = append(earlyCandidates, cand)
			}
		}
	}

	// Determine preference based on NormalPosition
	isUnseenPosition := ((req.NormalPosition - s.cfg.FirstUnseenPosition) % s.cfg.UnseenEveryNormalPositions) == 0

	var chosenGroup string
	var chosenCandidates []candidateUnit

	if isUnseenPosition {
		if len(unseenCandidates) > 0 {
			chosenGroup = "unseen"
			chosenCandidates = unseenCandidates
		} else if len(dueCandidates) > 0 {
			chosenGroup = "due"
			chosenCandidates = dueCandidates
		} else if len(earlyCandidates) > 0 {
			chosenGroup = "early_review"
			chosenCandidates = earlyCandidates
		}
	} else {
		if len(dueCandidates) > 0 {
			chosenGroup = "due"
			chosenCandidates = dueCandidates
		} else if len(unseenCandidates) > 0 {
			chosenGroup = "unseen"
			chosenCandidates = unseenCandidates
		} else if len(earlyCandidates) > 0 {
			chosenGroup = "early_review"
			chosenCandidates = earlyCandidates
		}
	}

	if chosenGroup == "" || len(chosenCandidates) == 0 {
		return &SelectionResult{
			Status:              "unavailable",
			Reason:              "NO_ELIGIBLE_QUESTION",
			CooldownCount:       cooldownCount,
			ContentMissingCount: contentMissingCount,
			ExhaustedCount:      exhaustedCount,
		}, nil
	}

	// Task family ordering index
	taskOrder := map[string]int{
		learning.TaskMeaningRecognition: 0,
		learning.TaskReverseSelection:   1,
		learning.TaskContextSelection:   2,
	}

	// Sort chosen group according to frozen specs
	switch chosenGroup {
	case "due":
		sort.Slice(chosenCandidates, func(i, j int) bool {
			a, b := chosenCandidates[i], chosenCandidates[j]
			// 1. needs_consolidation DESC (recent error first)
			if a.needsConsolidation != b.needsConsolidation {
				return a.needsConsolidation
			}
			// 2. due_at ASC
			if a.dueAt != b.dueAt {
				return a.dueAt < b.dueAt
			}
			// 3. ordinary_samples ASC
			if a.ordinarySamples != b.ordinarySamples {
				return a.ordinarySamples < b.ordinarySamples
			}
			// 4. earliest_ordinary_observed_at ASC (missing first: 0 first)
			if a.earliestOrdinaryObservedAt != b.earliestOrdinaryObservedAt {
				return a.earliestOrdinaryObservedAt < b.earliestOrdinaryObservedAt
			}
			// 5. sense_id ASC
			if a.senseID != b.senseID {
				return a.senseID < b.senseID
			}
			// 6. task_family fixed enum order
			return taskOrder[a.taskFamily] < taskOrder[b.taskFamily]
		})

	case "unseen":
		sort.Slice(chosenCandidates, func(i, j int) bool {
			a, b := chosenCandidates[i], chosenCandidates[j]
			// 1. Same lexeme tested tasks count ASC
			if a.otherTestedTasksCount != b.otherTestedTasksCount {
				return a.otherTestedTasksCount < b.otherTestedTasksCount
			}
			// 2. sense_id ASC
			if a.senseID != b.senseID {
				return a.senseID < b.senseID
			}
			// 3. task_family fixed enum order
			return taskOrder[a.taskFamily] < taskOrder[b.taskFamily]
		})

	case "early_review":
		sort.Slice(chosenCandidates, func(i, j int) bool {
			a, b := chosenCandidates[i], chosenCandidates[j]
			// 1. due_at ASC
			if a.dueAt != b.dueAt {
				return a.dueAt < b.dueAt
			}
			// 2. ordinary_samples ASC
			if a.ordinarySamples != b.ordinarySamples {
				return a.ordinarySamples < b.ordinarySamples
			}
			// 3. sense_id ASC
			if a.senseID != b.senseID {
				return a.senseID < b.senseID
			}
			// 4. task_family fixed enum order
			return taskOrder[a.taskFamily] < taskOrder[b.taskFamily]
		})
	}

	topUnit := chosenCandidates[0]

	// Pick best form for the top unit
	allForms, err := s.getPublishedVersionsForUnit(ctx, topUnit.senseID, topUnit.taskFamily)
	if err != nil {
		return nil, fmt.Errorf("get forms for top unit: %w", err)
	}

	bestQV, err := s.pickBestForm(ctx, req.SessionID, req.ProjectionVersion, topUnit.senseID, topUnit.taskFamily, allForms)
	if err != nil {
		return nil, fmt.Errorf("pick best form: %w", err)
	}

	return &SelectionResult{
		Status:          "ready",
		CandidateGroup:  chosenGroup,
		IsCorrection:    false,
		QuestionVersion: bestQV,
		FormID:          bestQV.FormID,
		SenseID:         topUnit.senseID,
		LexemeID:        topUnit.lexemeID,
		TaskFamily:      topUnit.taskFamily,
		SelectionReason: fmt.Sprintf("selected from %s group (position %d)", chosenGroup, req.NormalPosition),
	}, nil
}

func (s *Selector) getPublishedVersionsForUnit(ctx context.Context, senseID, taskFamily string) ([]*content.QuestionVersion, error) {
	query := `
		SELECT qv.id, qv.form_id, qv.sense_id, qv.sense_version_id, qv.task_family,
		       qv.stem, qv.options_json, qv.correct_option_id, qv.explanation,
		       qv.candidate_id, qv.status, qv.revision, qv.created_at, qv.updated_at
		FROM question_version qv
		WHERE qv.sense_id = ? AND qv.task_family = ? AND qv.status = 'published'
		ORDER BY qv.created_at DESC
	`
	rows, err := s.db.QueryContext(ctx, query, senseID, taskFamily)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var versions []*content.QuestionVersion
	seenForms := make(map[string]bool)

	for rows.Next() {
		var qv content.QuestionVersion
		var optionsJSON string
		var candidateID sql.NullString
		err := rows.Scan(
			&qv.ID,
			&qv.FormID,
			&qv.SenseID,
			&qv.SenseVersionID,
			&qv.TaskFamily,
			&qv.Stem,
			&optionsJSON,
			&qv.CorrectOptionID,
			&qv.Explanation,
			&candidateID,
			&qv.Status,
			&qv.Revision,
			&qv.CreatedAt,
			&qv.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		if candidateID.Valid {
			qv.CandidateID = &candidateID.String
		}
		if err := json.Unmarshal([]byte(optionsJSON), &qv.Options); err != nil {
			return nil, fmt.Errorf("unmarshal options for question version %s: %w", qv.ID, err)
		}

		// Group by FormID to take latest published version per form
		if !seenForms[qv.FormID] {
			seenForms[qv.FormID] = true
			v := qv
			versions = append(versions, &v)
		}
	}
	return versions, rows.Err()
}

func (s *Selector) getAnsweredFormsInSession(ctx context.Context, sessionID, senseID, taskFamily string) (map[string]bool, error) {
	query := `
		SELECT DISTINCT qv.form_id
		FROM attempt_event a
		JOIN question_instance qi ON qi.id = a.instance_id
		JOIN question_version qv ON qv.id = qi.question_version_id
		WHERE a.session_id = ? AND a.sense_id = ? AND a.task_family = ?
	`
	rows, err := s.db.QueryContext(ctx, query, sessionID, senseID, taskFamily)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := make(map[string]bool)
	for rows.Next() {
		var formID string
		if err := rows.Scan(&formID); err != nil {
			return nil, err
		}
		res[formID] = true
	}
	return res, rows.Err()
}

type formCandidate struct {
	qv                   *content.QuestionVersion
	unusedInSession      bool
	lastReservedAt       int64
	hasDistractorConfusion bool
}

func (s *Selector) pickBestForm(
	ctx context.Context,
	sessionID string,
	projectionVersion string,
	senseID string,
	taskFamily string,
	forms []*content.QuestionVersion,
) (*content.QuestionVersion, error) {
	if len(forms) == 1 {
		return forms[0], nil
	}

	answered, err := s.getAnsweredFormsInSession(ctx, sessionID, senseID, taskFamily)
	if err != nil {
		return nil, err
	}

	candidates := make([]formCandidate, len(forms))
	for i, f := range forms {
		c := formCandidate{
			qv:              f,
			unusedInSession: !answered[f.FormID],
		}

		// Query last reserved exposure for this form
		queryLastRes := `
			SELECT max(observed_at)
			FROM exposure_event
			WHERE form_id = ? AND kind = 'reserved'
		`
		var lastRes sql.NullInt64
		_ = s.db.QueryRowContext(ctx, queryLastRes, f.FormID).Scan(&lastRes)
		if lastRes.Valid {
			c.lastReservedAt = lastRes.Int64
		}

		// Check if any option in the form has >= confusion_min_wrong_selections
		for _, opt := range f.Options {
			if opt.ID == f.CorrectOptionID || opt.OptionIdentityID == "" {
				continue
			}
			var wrongCount int
			queryConf := `
				SELECT wrong_selected_count
				FROM confusion_projection
				WHERE projection_version = ?
				  AND target_sense_id = ?
				  AND task_family = ?
				  AND distractor_identity_id = ?
				  AND stat_group = 'ordinary'
			`
			_ = s.db.QueryRowContext(ctx, queryConf, projectionVersion, senseID, taskFamily, opt.OptionIdentityID).Scan(&wrongCount)
			if wrongCount >= s.cfg.ConfusionMinWrongSelections {
				c.hasDistractorConfusion = true
				break
			}
		}

		candidates[i] = c
	}

	// Sort form candidates:
	// 1. unusedInSession DESC
	// 2. lastReservedAt ASC (least recently reserved; 0 first)
	// 3. hasDistractorConfusion DESC
	// 4. form_id ASC
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.unusedInSession != b.unusedInSession {
			return a.unusedInSession
		}
		if a.lastReservedAt != b.lastReservedAt {
			return a.lastReservedAt < b.lastReservedAt
		}
		if a.hasDistractorConfusion != b.hasDistractorConfusion {
			return a.hasDistractorConfusion
		}
		return a.qv.FormID < b.qv.FormID
	})

	return candidates[0].qv, nil
}
