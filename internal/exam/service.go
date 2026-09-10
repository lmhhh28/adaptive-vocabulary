package exam

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"adaptive-vocabulary/internal/core"
	"adaptive-vocabulary/internal/learning"
)

var (
	ErrExamNotFound          = errors.New("exam session not found")
	ErrBankNotFound          = errors.New("bank not found")
	ErrBankArchived          = errors.New("bank is archived")
	ErrSessionClosed         = errors.New("exam session is closed")
	ErrExamSubmittedDeadline = errors.New("exam submitted due to deadline expiration")
	ErrInstanceNotFound      = errors.New("question instance not found in this exam")
	ErrInvalidOption         = errors.New("invalid option for this question")
	ErrInvalidRequest        = errors.New("invalid request")
	ErrMaintenance           = errors.New("system under maintenance")
	ErrPreconditionRequired  = errors.New("precondition required")
	ErrExamNotSubmitted      = errors.New("exam has not been submitted")
)

type ActiveSessionConflictError struct {
	ActiveSession *core.Session
}

func (e *ActiveSessionConflictError) Error() string {
	return fmt.Sprintf("active session exists with id %s (%s)", e.ActiveSession.ID, e.ActiveSession.Kind)
}

type RevisionConflictNotAcceptedError struct {
	CurrentRevision int64
	LastAckSequence int64
}

func (e *RevisionConflictNotAcceptedError) Error() string {
	return fmt.Sprintf("revision conflict: current revision is %d, last ack sequence is %d", e.CurrentRevision, e.LastAckSequence)
}

type SequenceConflictNotAcceptedError struct {
	CurrentRevision int64
	LastAckSequence int64
}

func (e *SequenceConflictNotAcceptedError) Error() string {
	return fmt.Sprintf("sequence conflict: sequence already used or invalid")
}

type SequenceGapNotAcceptedError struct {
	ExpectedNextSequence int64
	LastAckSequence      int64
}

func (e *SequenceGapNotAcceptedError) Error() string {
	return fmt.Sprintf("sequence gap: expected next sequence is %d", e.ExpectedNextSequence)
}

type Service struct {
	db              *sql.DB
	learningService *learning.LearningService
	assembler       *Assembler
	maxQuestions    int
}

func NewService(db *sql.DB, learningService *learning.LearningService, assembler *Assembler) *Service {
	return NewServiceWithPolicy(db, learningService, assembler, DefaultExamPolicyConfig())
}

func NewServiceWithPolicy(db *sql.DB, learningService *learning.LearningService, assembler *Assembler, policy ExamPolicyConfig) *Service {
	if assembler == nil {
		maxSearch := policy.MaxSearchStates
		if maxSearch <= 0 {
			maxSearch = 10000
		}
		assembler = NewAssembler(db, maxSearch)
	}
	maxQ := policy.MaxQuestions
	if maxQ <= 0 {
		maxQ = 200
	}
	return &Service{
		db:              db,
		learningService: learningService,
		assembler:       assembler,
		maxQuestions:    maxQ,
	}
}

func saveIdempotency(tc *core.TxnContext, op, scope, key, digest, status string, code int, body string, nowMicro int64) error {
	return core.SaveIdempotencyRecord(tc, &core.IdempotencyRecord{
		Operation:     op,
		ResourceScope: scope,
		Key:           key,
		RequestDigest: digest,
		Status:        status,
		ResponseCode:  code,
		ResponseBody:  body,
		CreatedAt:     nowMicro,
	})
}

// CreateExam handles POST /api/v1/exams.
func (s *Service) CreateExam(
	ctx context.Context,
	req CreateExamRequest,
	idempotencyKey string,
	requestDigest string,
) (*CreateExamResponse, bool, error) {
	if req.QuestionCount <= 0 {
		return nil, false, fmt.Errorf("%w: question_count must be positive", ErrInvalidRequest)
	}
	if req.QuestionCount > s.maxQuestions {
		return nil, false, fmt.Errorf("%w: question_count exceeds maximum allowed of %d", ErrInvalidRequest, s.maxQuestions)
	}
	if req.TimeLimitSeconds != nil {
		if *req.TimeLimitSeconds < 60 || *req.TimeLimitSeconds > 14400 {
			return nil, false, fmt.Errorf("%w: time_limit_seconds must be between 60 and 14400", ErrInvalidRequest)
		}
	}

	quotas, err := ResolveQuotas(req.QuestionCount, req.TaskQuotas)
	if err != nil {
		return nil, false, err
	}

	var (
		res      *CreateExamResponse
		isReplay bool
	)

	err = core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		// 1. Maintenance check
		if err := core.CheckMaintenance(tc); err != nil {
			return ErrMaintenance
		}

		// 2. Validate bank
		var bankArchived int
		err := tc.QueryRow("SELECT archived FROM bank WHERE id = ?", req.BankID).Scan(&bankArchived)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBankNotFound
		}
		if err != nil {
			return fmt.Errorf("lookup bank: %w", err)
		}
		if bankArchived == 1 {
			return ErrBankArchived
		}

		// 3. Check for existing active session (across all modes)
		activeSess, err := core.GetActiveSession(tc)
		if err != nil {
			return fmt.Errorf("check active session: %w", err)
		}
		if activeSess != nil {
			// If idempotency matches the active session creation, replay
			if idempotencyKey != "" {
				rec, err := core.CheckIdempotency(tc, "POST", "/api/v1/exams", idempotencyKey, requestDigest)
				if errors.Is(err, core.ErrIdempotencyConflict) {
					return err
				}
				if rec != nil {
					var savedRes CreateExamResponse
					if err := json.Unmarshal([]byte(rec.ResponseBody), &savedRes); err == nil {
						res = &savedRes
						isReplay = true
						return nil
					}
				}
			}
			return &ActiveSessionConflictError{ActiveSession: activeSess}
		}

		// 4. Check idempotency for completed/abandoned sessions
		if idempotencyKey != "" {
			rec, err := core.CheckIdempotency(tc, "POST", "/api/v1/exams", idempotencyKey, requestDigest)
			if errors.Is(err, core.ErrIdempotencyConflict) {
				return err
			}
			if rec != nil {
				var savedRes CreateExamResponse
				if err := json.Unmarshal([]byte(rec.ResponseBody), &savedRes); err == nil {
					res = &savedRes
					isReplay = true
					return nil
				}
			}
		}

		// 5. Assemble paper
		candidates, err := s.assembler.Assemble(tc, req.BankID, quotas)
		if err != nil {
			return err
		}

		// 6. Create session
		sessionID := core.NewUUID()
		nowMicro := time.Now().UTC().UnixNano() / 1000
		var deadlineMicro *int64
		var deadlineStr *string
		if req.TimeLimitSeconds != nil {
			dl := nowMicro + int64(*req.TimeLimitSeconds)*1000000
			deadlineMicro = &dl
			str := time.Unix(0, dl*1000).UTC().Format(time.RFC3339)
			deadlineStr = &str
		}

		insertSession := `
			INSERT INTO session (id, kind, status, bank_id, cursor, normal_position, revision, last_ack_sequence, deadline_at, created_at, updated_at)
			VALUES (?, 'exam', 'open', ?, 0, NULL, 0, 0, ?, ?, ?)
		`
		if _, err := tc.Exec(insertSession, sessionID, req.BankID, deadlineMicro, nowMicro, nowMicro); err != nil {
			return fmt.Errorf("insert session: %w", err)
		}

		quotasJSON, _ := json.Marshal(quotas)
		var tagsJSON *string
		if len(req.Tags) > 0 {
			b, _ := json.Marshal(req.Tags)
			s := string(b)
			tagsJSON = &s
		}

		insertExamSession := `
			INSERT INTO exam_session (session_id, bank_id, question_count, time_limit_seconds, deadline_at, task_quotas_json, tags_json, telemetry_incomplete, assembly_seed, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 0, 'core', ?, ?)
		`
		if _, err := tc.Exec(insertExamSession, sessionID, req.BankID, len(candidates), req.TimeLimitSeconds, deadlineMicro, string(quotasJSON), tagsJSON, nowMicro, nowMicro); err != nil {
			return fmt.Errorf("insert exam_session: %w", err)
		}

		var publicQuestions []PublicQuestion
		var drafts []ExamDraftDTO

		// 7. Create question instances, drafts, and reserved exposures
		for ordinal, cand := range candidates {
			instanceID := core.NewUUID()

			snapshot := InstanceSnapshot{
				Stem:            cand.Stem,
				Options:         cand.Options,
				CorrectOptionID: cand.CorrectOptionID,
				Explanation:     cand.Explanation,
				Purpose:         "exam",
				SenseID:         cand.SenseID,
				LexemeID:        cand.LexemeID,
				FormID:          cand.FormID,
				TaskFamily:      cand.TaskFamily,
			}
			snapJSON, _ := json.Marshal(snapshot)

			insertInst := `
				INSERT INTO question_instance (id, session_id, ordinal, question_version_id, snapshot_json, normal_position, created_at)
				VALUES (?, ?, ?, ?, ?, NULL, ?)
			`
			if _, err := tc.Exec(insertInst, instanceID, sessionID, ordinal, cand.ID, string(snapJSON), nowMicro); err != nil {
				return fmt.Errorf("insert question_instance: %w", err)
			}

			// Reserved exposure event
			expID := core.NewUUID()
			resSeq, err := tc.Exec("INSERT INTO global_sequence (event_name, created_at) VALUES ('exposure_reserved', ?)", nowMicro)
			if err != nil {
				return fmt.Errorf("allocate exposure seq: %w", err)
			}
			seqID, _ := resSeq.LastInsertId()

			insertExp := `
				INSERT INTO exposure_event (id, source_instance_id, kind, sense_id, lexeme_id, form_id, observed_at, event_sequence, created_at)
				VALUES (?, ?, 'reserved', ?, ?, ?, ?, ?, ?)
			`
			if _, err := tc.Exec(insertExp, expID, instanceID, cand.SenseID, cand.LexemeID, cand.FormID, nowMicro, seqID, nowMicro); err != nil {
				return fmt.Errorf("insert reserved exposure: %w", err)
			}

			// Draft initialization
			insertDraft := `
				INSERT INTO exam_instance_draft (session_id, instance_id, selected_option_id, revision_count, is_first_reliable, updated_at)
				VALUES (?, ?, NULL, 0, 1, ?)
			`
			if _, err := tc.Exec(insertDraft, sessionID, instanceID, nowMicro); err != nil {
				return fmt.Errorf("insert draft: %w", err)
			}

			var pubOpts []PublicOption
			for _, opt := range cand.Options {
				pubOpts = append(pubOpts, PublicOption{
					ID:   opt.ID,
					Text: opt.Text,
				})
			}

			publicQuestions = append(publicQuestions, PublicQuestion{
				InstanceID: instanceID,
				Ordinal:    int64(ordinal),
				TaskFamily: cand.TaskFamily,
				Stem:       cand.Stem,
				Options:    pubOpts,
			})

			drafts = append(drafts, ExamDraftDTO{
				InstanceID:       instanceID,
				SelectedOptionID: nil,
				RevisionCount:    0,
			})
		}

		res = &CreateExamResponse{
			ExamID:          sessionID,
			SessionRevision: 0,
			Deadline:        deadlineStr,
			PublicQuestions: publicQuestions,
			Drafts:          drafts,
			LastAckSequence: 0,
		}

		// 8. Save idempotency record
		if idempotencyKey != "" {
			bodyJSON, _ := json.Marshal(res)
			if err := saveIdempotency(tc, "POST", "/api/v1/exams", idempotencyKey, requestDigest, "completed", 201, string(bodyJSON), nowMicro); err != nil {
				return fmt.Errorf("save idempotency: %w", err)
			}
		}

		return nil
	})

	if err != nil {
		return nil, false, err
	}

	return res, isReplay, nil
}

// GetExam handles GET /api/v1/exams/{id}.
func (s *Service) GetExam(ctx context.Context, examID string) (*ExamDetailResponse, error) {
	var (
		status          string
		revision        int64
		lastAckSequence int64
		deadlineAt      sql.NullInt64
	)

	querySess := `SELECT status, revision, last_ack_sequence, deadline_at FROM session WHERE id = ? AND kind = 'exam'`
	err := s.db.QueryRowContext(ctx, querySess, examID).Scan(&status, &revision, &lastAckSequence, &deadlineAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrExamNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query exam session: %w", err)
	}

	var deadlineStr *string
	if deadlineAt.Valid {
		str := time.Unix(0, deadlineAt.Int64*1000).UTC().Format(time.RFC3339)
		deadlineStr = &str
	}

	// Load question instances and drafts
	queryInst := `
		SELECT qi.id, qi.ordinal, qi.snapshot_json, d.selected_option_id, d.revision_count
		FROM question_instance qi
		JOIN exam_instance_draft d ON d.instance_id = qi.id AND d.session_id = qi.session_id
		WHERE qi.session_id = ?
		ORDER BY qi.ordinal ASC
	`
	rows, err := s.db.QueryContext(ctx, queryInst, examID)
	if err != nil {
		return nil, fmt.Errorf("query exam instances: %w", err)
	}
	defer rows.Close()

	var pubQuestions []PublicQuestion
	var drafts []ExamDraftDTO

	for rows.Next() {
		var (
			instID        string
			ordinal       int64
			snapJSON      string
			selOptID      sql.NullString
			revisionCount int
		)
		if err := rows.Scan(&instID, &ordinal, &snapJSON, &selOptID, &revisionCount); err != nil {
			return nil, fmt.Errorf("scan instance: %w", err)
		}

		var snap InstanceSnapshot
		if err := json.Unmarshal([]byte(snapJSON), &snap); err != nil {
			return nil, fmt.Errorf("unmarshal instance snapshot for %s: %w", instID, err)
		}

		var pubOpts []PublicOption
		for _, opt := range snap.Options {
			pubOpts = append(pubOpts, PublicOption{
				ID:   opt.ID,
				Text: opt.Text,
			})
		}

		pubQuestions = append(pubQuestions, PublicQuestion{
			InstanceID: instID,
			Ordinal:    ordinal,
			TaskFamily: snap.TaskFamily,
			Stem:       snap.Stem,
			Options:    pubOpts,
		})

		var optIDPtr *string
		if selOptID.Valid {
			optIDPtr = &selOptID.String
		}
		drafts = append(drafts, ExamDraftDTO{
			InstanceID:       instID,
			SelectedOptionID: optIDPtr,
			RevisionCount:    revisionCount,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate exam instances: %w", err)
	}

	var resultURL *string
	if status == "submitted" {
		url := fmt.Sprintf("/exams/%s/result", examID)
		resultURL = &url
	}

	return &ExamDetailResponse{
		ID:              examID,
		Status:          status,
		SessionRevision: revision,
		Deadline:        deadlineStr,
		PublicQuestions: pubQuestions,
		Drafts:          drafts,
		LastAckSequence: lastAckSequence,
		ResultURL:       resultURL,
	}, nil
}

// RecordResponseEvent handles POST /api/v1/exams/{id}/response-events according to 10 R01.
func (s *Service) RecordResponseEvent(
	ctx context.Context,
	examID string,
	req ResponseEventRequest,
	idempotencyKey string,
	requestDigest string,
) (*ResponseEventResponse, bool, error) {
	if req.Action != "select" && req.Action != "clear" {
		return nil, false, fmt.Errorf("%w: action must be select or clear", ErrInvalidRequest)
	}
	if req.Action == "select" && (req.OptionID == nil || *req.OptionID == "") {
		return nil, false, fmt.Errorf("%w: option_id is required for select action", ErrInvalidRequest)
	}
	if req.ClientEventID == "" {
		return nil, false, fmt.Errorf("%w: client_event_id is required", ErrInvalidRequest)
	}

	var (
		res      *ResponseEventResponse
		isReplay bool
	)
	deadlineSubmitted := false

	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		// Committed receipts remain readable after closure or maintenance.
		rec, err := core.CheckIdempotency(tc, "POST", fmt.Sprintf("/api/v1/exams/%s/response-events", examID), req.ClientEventID, requestDigest)
		if err != nil {
			return err
		}
		if rec != nil {
			if err := json.Unmarshal([]byte(rec.ResponseBody), &res); err != nil {
				return err
			}
			isReplay = true
			return nil
		}
		// 1. Maintenance check
		if err := core.CheckMaintenance(tc); err != nil {
			return ErrMaintenance
		}

		// 2. Load session with row lock
		var (
			status          string
			currentRevision int64
			lastAckSequence int64
			deadlineAt      sql.NullInt64
		)
		querySess := `SELECT status, revision, last_ack_sequence, deadline_at FROM session WHERE id = ? AND kind = 'exam'`
		err = tc.QueryRow(querySess, examID).Scan(&status, &currentRevision, &lastAckSequence, &deadlineAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrExamNotFound
		}
		if err != nil {
			return fmt.Errorf("load exam session: %w", err)
		}

		if status != "open" {
			return ErrSessionClosed
		}

		nowMicro := time.Now().UTC().UnixNano() / 1000

		// 3. Deadline check -> auto-submit if expired
		if deadlineAt.Valid && nowMicro >= deadlineAt.Int64 {
			if _, err := s.submitExamInternal(tc, examID, "auto_deadline", true); err != nil {
				return err
			}
			// Commit settlement before rejecting the late action.
			deadlineSubmitted = true
			return nil
		}

		// 5. Validate expected_revision
		if req.ExpectedRevision != currentRevision {
			// Explicit rejection per 10 R01 (acceptance = not_accepted)
			return &RevisionConflictNotAcceptedError{
				CurrentRevision: currentRevision,
				LastAckSequence: lastAckSequence,
			}
		}

		// 6. Validate client_sequence
		expectedSeq := lastAckSequence + 1
		if req.ClientSequence > expectedSeq {
			return &SequenceGapNotAcceptedError{
				ExpectedNextSequence: expectedSeq,
				LastAckSequence:      lastAckSequence,
			}
		}
		if req.ClientSequence < expectedSeq {
			return &SequenceConflictNotAcceptedError{
				CurrentRevision: currentRevision,
				LastAckSequence: lastAckSequence,
			}
		}

		// 7. Load instance and current draft
		var (
			curSelectedOptID sql.NullString
			firstOptID       sql.NullString
			revisionCount    int
			isFirstReliable  int
			snapJSON         string
		)
		queryDraft := `
			SELECT d.selected_option_id, d.first_selected_option_id, d.revision_count, d.is_first_reliable, qi.snapshot_json
			FROM exam_instance_draft d
			JOIN question_instance qi ON qi.id = d.instance_id AND qi.session_id = d.session_id
			WHERE d.session_id = ? AND d.instance_id = ?
		`
		err = tc.QueryRow(queryDraft, examID, req.InstanceID).Scan(&curSelectedOptID, &firstOptID, &revisionCount, &isFirstReliable, &snapJSON)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInstanceNotFound
		}
		if err != nil {
			return fmt.Errorf("query draft: %w", err)
		}

		var snap InstanceSnapshot
		if err := json.Unmarshal([]byte(snapJSON), &snap); err != nil {
			return fmt.Errorf("unmarshal instance snapshot: %w", err)
		}

		// If selecting, verify option is in question
		if req.Action == "select" {
			valid := false
			for _, opt := range snap.Options {
				if opt.ID == *req.OptionID {
					valid = true
					break
				}
			}
			if !valid {
				return ErrInvalidOption
			}
		}

		// 8. Determine if noop or state update
		var isNoop bool
		var newSelectedOptID *string
		if req.Action == "clear" {
			if !curSelectedOptID.Valid {
				isNoop = true
			}
			newSelectedOptID = nil
		} else { // "select"
			if curSelectedOptID.Valid && curSelectedOptID.String == *req.OptionID {
				isNoop = true
			}
			newSelectedOptID = req.OptionID
		}

		eventID := core.NewUUID()
		actionStr := req.Action
		if isNoop {
			actionStr = "noop"
		}

		// Update draft if substantive change
		if !isNoop {
			if req.Action == "clear" {
				if curSelectedOptID.Valid {
					revisionCount++
					isFirstReliable = 0
				}
			} else { // "select"
				if !firstOptID.Valid {
					// First selection
					firstOptID = sql.NullString{String: *req.OptionID, Valid: true}
				} else if curSelectedOptID.Valid && curSelectedOptID.String != *req.OptionID {
					// Changed answer
					revisionCount++
				}
			}

			updateDraft := `
				UPDATE exam_instance_draft
				SET selected_option_id = ?, first_selected_option_id = ?, revision_count = ?, is_first_reliable = ?, final_response_event_id = ?, updated_at = ?
				WHERE session_id = ? AND instance_id = ?
			`
			if _, err := tc.Exec(updateDraft, newSelectedOptID, firstOptID, revisionCount, isFirstReliable, eventID, nowMicro, examID, req.InstanceID); err != nil {
				return fmt.Errorf("update draft: %w", err)
			}
		}

		// 9. Allocate global sequence & insert response_event
		resSeq, err := tc.Exec("INSERT INTO global_sequence (event_name, created_at) VALUES ('response_event', ?)", nowMicro)
		if err != nil {
			return fmt.Errorf("allocate response seq: %w", err)
		}
		seqID, _ := resSeq.LastInsertId()

		newRevision := currentRevision + 1
		acceptedSeq := req.ClientSequence

		var rebasedFromStr *string
		if req.RebasedFrom != nil {
			b, _ := json.Marshal(req.RebasedFrom)
			s := string(b)
			rebasedFromStr = &s
		}

		insertEvent := `
			INSERT INTO response_event (
				id, session_id, client_event_id, client_sequence, instance_id,
				action, option_id, expected_revision, accepted_revision, rebased_from,
				observed_at, event_sequence, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`
		if _, err := tc.Exec(insertEvent, eventID, examID, req.ClientEventID, acceptedSeq, req.InstanceID, actionStr, req.OptionID, req.ExpectedRevision, newRevision, rebasedFromStr, nowMicro, seqID, nowMicro); err != nil {
			return fmt.Errorf("insert response_event: %w", err)
		}

		// 10. Increment session revision and last_ack_sequence
		updateSess := `
			UPDATE session
			SET revision = ?, last_ack_sequence = ?, updated_at = ?
			WHERE id = ?
		`
		if _, err := tc.Exec(updateSess, newRevision, acceptedSeq, nowMicro, examID); err != nil {
			return fmt.Errorf("update session revision: %w", err)
		}

		res = &ResponseEventResponse{
			EventID:          eventID,
			AcceptedSequence: acceptedSeq,
			CurrentRevision:  newRevision,
			Draft: ExamDraftDTO{
				InstanceID:       req.InstanceID,
				SelectedOptionID: newSelectedOptID,
				RevisionCount:    revisionCount,
			},
		}

		// 11. Save idempotency record
		bodyJSON, _ := json.Marshal(res)
		if err := saveIdempotency(tc, "POST", fmt.Sprintf("/api/v1/exams/%s/response-events", examID), req.ClientEventID, requestDigest, "completed", 200, string(bodyJSON), nowMicro); err != nil {
			return fmt.Errorf("save idempotency: %w", err)
		}

		return nil
	})

	if err != nil {
		return nil, false, err
	}

	if deadlineSubmitted {
		return nil, false, ErrExamSubmittedDeadline
	}
	return res, isReplay, nil
}

// GetResponseEventReceipt handles GET /api/v1/exams/{id}/response-events/{client_event_id}.
func (s *Service) GetResponseEventReceipt(ctx context.Context, examID, clientEventID string) (*EventReceiptResponse, error) {
	query := `
		SELECT id, client_event_id, client_sequence, accepted_revision, instance_id, action, option_id
		FROM response_event
		WHERE session_id = ? AND client_event_id = ?
	`
	var (
		eventID  string
		cEventID string
		cSeq     int64
		accRev   int64
		instID   string
		action   string
		optID    sql.NullString
	)
	err := s.db.QueryRowContext(ctx, query, examID, clientEventID).Scan(&eventID, &cEventID, &cSeq, &accRev, &instID, &action, &optID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("event receipt not found")
	}
	if err != nil {
		return nil, fmt.Errorf("query response event receipt: %w", err)
	}

	var optIDPtr *string
	if optID.Valid {
		optIDPtr = &optID.String
	}

	return &EventReceiptResponse{
		EventID:          eventID,
		ClientEventID:    cEventID,
		ClientSequence:   cSeq,
		AcceptedRevision: accRev,
		InstanceID:       instID,
		Action:           action,
		OptionID:         optIDPtr,
	}, nil
}

// RecordTelemetryGap handles POST /api/v1/exams/{id}/telemetry-gaps (CAS barrier).
func (s *Service) RecordTelemetryGap(
	ctx context.Context,
	examID string,
	req TelemetryGapsRequest,
	idempotencyKey string,
	requestDigest string,
) (*TelemetryGapsResponse, error) {
	var res *TelemetryGapsResponse

	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		// Check idempotency first before session state check
		if idempotencyKey != "" {
			rec, err := core.CheckIdempotency(tc, "POST", fmt.Sprintf("/api/v1/exams/%s/telemetry-gaps", examID), idempotencyKey, requestDigest)
			if errors.Is(err, core.ErrIdempotencyConflict) {
				return err
			}
			if rec != nil {
				var savedRes TelemetryGapsResponse
				if err := json.Unmarshal([]byte(rec.ResponseBody), &savedRes); err == nil {
					res = &savedRes
					return nil
				}
			}
		}

		if err := core.CheckMaintenance(tc); err != nil {
			return ErrMaintenance
		}

		var (
			status          string
			currentRevision int64
			lastAckSequence int64
		)
		querySess := `SELECT status, revision, last_ack_sequence FROM session WHERE id = ? AND kind = 'exam'`
		err := tc.QueryRow(querySess, examID).Scan(&status, &currentRevision, &lastAckSequence)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrExamNotFound
		}
		if err != nil {
			return err
		}
		if status != "open" {
			return ErrSessionClosed
		}

		// CAS check on expected_revision
		if req.ExpectedRevision != currentRevision {
			return &RevisionConflictNotAcceptedError{
				CurrentRevision: currentRevision,
				LastAckSequence: lastAckSequence,
			}
		}

		nowMicro := time.Now().UTC().UnixNano() / 1000
		newRevision := currentRevision + 1

		// Set telemetry_incomplete
		if _, err := tc.Exec("UPDATE exam_session SET telemetry_incomplete = 1, updated_at = ? WHERE session_id = ?", nowMicro, examID); err != nil {
			return fmt.Errorf("update exam_session telemetry_incomplete: %w", err)
		}

		// Advance revision
		if _, err := tc.Exec("UPDATE session SET revision = ?, updated_at = ? WHERE id = ?", newRevision, nowMicro, examID); err != nil {
			return fmt.Errorf("advance session revision: %w", err)
		}

		res = &TelemetryGapsResponse{
			TelemetryIncomplete: true,
			CurrentRevision:     newRevision,
			LastAckSequence:     lastAckSequence,
		}

		if idempotencyKey != "" {
			bodyJSON, _ := json.Marshal(res)
			if err := saveIdempotency(tc, "POST", fmt.Sprintf("/api/v1/exams/%s/telemetry-gaps", examID), idempotencyKey, requestDigest, "completed", 200, string(bodyJSON), nowMicro); err != nil {
				return fmt.Errorf("save idempotency: %w", err)
			}
		}

		return nil
	})

	return res, err
}

// SubmitExam handles POST /api/v1/exams/{id}/submit.
func (s *Service) SubmitExam(
	ctx context.Context,
	examID string,
	req SubmitExamRequest,
	idempotencyKey string,
	requestDigest string,
) (*SubmitExamResponse, bool, error) {
	var (
		res      *SubmitExamResponse
		isReplay bool
	)

	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		if idempotencyKey != "" {
			rec, err := core.CheckIdempotency(tc, "POST", fmt.Sprintf("/api/v1/exams/%s/submit", examID), idempotencyKey, requestDigest)
			if err != nil {
				return err
			}
			if rec != nil {
				if err := json.Unmarshal([]byte(rec.ResponseBody), &res); err != nil {
					return err
				}
				isReplay = true
				return nil
			}
		}
		if err := core.CheckMaintenance(tc); err != nil {
			return ErrMaintenance
		}

		nowMicro := time.Now().UTC().UnixNano() / 1000

		var (
			status          string
			currentRevision int64
			lastAckSequence int64
		)
		querySess := `SELECT status, revision, last_ack_sequence FROM session WHERE id = ? AND kind = 'exam'`
		err := tc.QueryRow(querySess, examID).Scan(&status, &currentRevision, &lastAckSequence)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrExamNotFound
		}
		if err != nil {
			return err
		}

		// If already submitted, check idempotency and return existing result
		if status == "submitted" {
			var assessID string
			var resRev int64
			err := tc.QueryRow("SELECT id, result_revision FROM assessment_result WHERE session_id = ?", examID).Scan(&assessID, &resRev)
			if err == nil {
				res = &SubmitExamResponse{
					AssessmentID:   assessID,
					Status:         "submitted",
					ResultRevision: resRev,
					ResultURL:      fmt.Sprintf("/exams/%s/result", examID),
				}
				isReplay = true
				return nil
			}
			return ErrSessionClosed
		}
		if status != "open" {
			return ErrSessionClosed
		}

		// Precondition checks
		var deadlineAt sql.NullInt64
		if err := tc.QueryRow("SELECT deadline_at FROM session WHERE id = ?", examID).Scan(&deadlineAt); err != nil {
			return err
		}
		expired := deadlineAt.Valid && nowMicro >= deadlineAt.Int64
		if !expired && req.ExpectedRevision != currentRevision {
			return &RevisionConflictNotAcceptedError{
				CurrentRevision: currentRevision,
				LastAckSequence: lastAckSequence,
			}
		}
		if !expired && req.LastAckSequence < lastAckSequence {
			return fmt.Errorf("%w: unacknowledged events exist (client=%d, server=%d)", ErrPreconditionRequired, req.LastAckSequence, lastAckSequence)
		}

		// Settle exam
		assessmentID, err := s.submitExamInternal(tc, examID, req.SubmissionID, expired)
		if err != nil {
			return err
		}

		res = &SubmitExamResponse{
			AssessmentID:   assessmentID,
			Status:         "submitted",
			ResultRevision: 0,
			ResultURL:      fmt.Sprintf("/exams/%s/result", examID),
		}

		if idempotencyKey != "" {
			bodyJSON, _ := json.Marshal(res)
			if err := saveIdempotency(tc, "POST", fmt.Sprintf("/api/v1/exams/%s/submit", examID), idempotencyKey, requestDigest, "completed", 200, string(bodyJSON), nowMicro); err != nil {
				return fmt.Errorf("save idempotency: %w", err)
			}
		}

		return nil
	})

	if err != nil {
		return nil, false, err
	}

	return res, isReplay, nil
}

// submitExamInternal performs the atomic assessment calculation, feedback_available insertion,
// and FSRS attempt recording in tc.
func (s *Service) submitExamInternal(tc *core.TxnContext, examID string, submissionID string, isAutoDeadline bool) (string, error) {
	nowMicro := time.Now().UTC().UnixNano() / 1000

	var (
		telemetryIncomplete int
	)
	_ = tc.QueryRow("SELECT telemetry_incomplete FROM exam_session WHERE session_id = ?", examID).Scan(&telemetryIncomplete)
	if isAutoDeadline {
		telemetryIncomplete = 1
		_, _ = tc.Exec("UPDATE exam_session SET telemetry_incomplete = 1, updated_at = ? WHERE session_id = ?", nowMicro, examID)
	}

	// Load all instances and drafts
	query := `
		SELECT qi.id, qi.ordinal, qi.snapshot_json, d.selected_option_id, d.first_selected_option_id,
		       d.revision_count, d.is_first_reliable, d.first_response_event_id, d.final_response_event_id
		FROM question_instance qi
		JOIN exam_instance_draft d ON d.instance_id = qi.id AND d.session_id = qi.session_id
		WHERE qi.session_id = ?
		ORDER BY qi.ordinal ASC
	`
	rows, err := tc.Query(query, examID)
	if err != nil {
		return "", fmt.Errorf("query instances for submit: %w", err)
	}
	defer rows.Close()

	type ItemData struct {
		InstID           string
		Ordinal          int64
		Snapshot         InstanceSnapshot
		SelectedOptionID sql.NullString
		FirstOptionID    sql.NullString
		RevisionCount    int
		IsFirstReliable  int
		FirstEventID     sql.NullString
		FinalEventID     sql.NullString
		IsCorrect        bool
		FirstCorrect     *bool
		ResponseHistory  string
	}

	var items []ItemData
	totalCount := 0
	correctCount := 0
	omittedCount := 0

	for rows.Next() {
		var it ItemData
		var snapJSON string
		if err := rows.Scan(&it.InstID, &it.Ordinal, &snapJSON, &it.SelectedOptionID, &it.FirstOptionID, &it.RevisionCount, &it.IsFirstReliable, &it.FirstEventID, &it.FinalEventID); err != nil {
			return "", err
		}
		if err := json.Unmarshal([]byte(snapJSON), &it.Snapshot); err != nil {
			return "", fmt.Errorf("unmarshal item snapshot %s: %w", it.InstID, err)
		}

		totalCount++
		if !it.SelectedOptionID.Valid {
			omittedCount++
			it.IsCorrect = false
		} else {
			if it.SelectedOptionID.String == it.Snapshot.CorrectOptionID {
				it.IsCorrect = true
				correctCount++
			} else {
				it.IsCorrect = false
			}
		}

		// First choice correctness
		if it.FirstOptionID.Valid && it.IsFirstReliable == 1 {
			fc := (it.FirstOptionID.String == it.Snapshot.CorrectOptionID)
			it.FirstCorrect = &fc
		}

		// Determine response_history
		if telemetryIncomplete == 1 {
			it.ResponseHistory = "telemetry_incomplete"
		} else if !it.SelectedOptionID.Valid {
			it.ResponseHistory = "omitted"
		} else if it.RevisionCount > 0 {
			it.ResponseHistory = "revised"
		} else {
			it.ResponseHistory = "single_response"
		}

		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate items for settlement: %w", err)
	}

	var ratio *float64
	if totalCount > 0 {
		r := float64(correctCount) / float64(totalCount)
		ratio = &r
	}

	assessmentID := core.NewUUID()
	insertAssess := `
		INSERT INTO assessment_result (id, session_id, total_count, correct_count, omitted_count, void_count, ratio, result_revision, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, 0, ?, ?)
	`
	if _, err := tc.Exec(insertAssess, assessmentID, examID, totalCount, correctCount, omittedCount, ratio, nowMicro, nowMicro); err != nil {
		return "", fmt.Errorf("insert assessment_result: %w", err)
	}

	for _, it := range items {
		itemID := core.NewUUID()
		var firstCorrInt *int
		if it.FirstCorrect != nil {
			v := 0
			if *it.FirstCorrect {
				v = 1
			}
			firstCorrInt = &v
		}
		isCorrInt := 0
		if it.IsCorrect {
			isCorrInt = 1
		}

		var selOptPtr *string
		if it.SelectedOptionID.Valid {
			selOptPtr = &it.SelectedOptionID.String
		}

		insertItem := `
			INSERT INTO assessment_item_result (
				id, assessment_id, instance_id, sense_id, task_family,
				selected_option_id, correct_option_id, is_correct, response_history, first_correct, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`
		if _, err := tc.Exec(insertItem, itemID, assessmentID, it.InstID, it.Snapshot.SenseID, it.Snapshot.TaskFamily, selOptPtr, it.Snapshot.CorrectOptionID, isCorrInt, it.ResponseHistory, firstCorrInt, nowMicro); err != nil {
			return "", fmt.Errorf("insert assessment_item_result: %w", err)
		}

		// Insert feedback_available exposure for each question at submission time
		expID := core.NewUUID()
		resSeq, err := tc.Exec("INSERT INTO global_sequence (event_name, created_at) VALUES ('exposure_feedback', ?)", nowMicro)
		if err != nil {
			return "", err
		}
		seqID, _ := resSeq.LastInsertId()

		insertExp := `
			INSERT INTO exposure_event (id, source_instance_id, kind, sense_id, lexeme_id, form_id, observed_at, event_sequence, created_at)
			VALUES (?, ?, 'feedback_available', ?, ?, ?, ?, ?, ?)
		`
		if _, err := tc.Exec(insertExp, expID, it.InstID, it.Snapshot.SenseID, it.Snapshot.LexemeID, it.Snapshot.FormID, nowMicro, seqID, nowMicro); err != nil {
			return "", fmt.Errorf("insert feedback exposure: %w", err)
		}

		// Apply learning observation if LearningService is available
		if s.learningService != nil {
			var firstEvID, finalEvID *string
			if it.FirstEventID.Valid {
				firstEvID = &it.FirstEventID.String
			}
			if it.FinalEventID.Valid {
				finalEvID = &it.FinalEventID.String
			}

			// For single_response, observed_at is the receipt time of the single response event
			observedAt := nowMicro
			if it.ResponseHistory == "single_response" && it.FirstEventID.Valid {
				var evObservedAt int64
				_ = tc.QueryRow("SELECT observed_at FROM response_event WHERE id = ?", it.FirstEventID.String).Scan(&evObservedAt)
				if evObservedAt > 0 {
					observedAt = evObservedAt
				}
			}

			attempt := &learning.AttemptEvent{
				InstanceID:           it.InstID,
				SessionID:            examID,
				SenseID:              it.Snapshot.SenseID,
				TaskFamily:           it.Snapshot.TaskFamily,
				Source:               "exam",
				Purpose:              "normal",
				ResponseHistory:      it.ResponseHistory,
				SelectedOptionID:     selOptPtr,
				FinalOptionID:        selOptPtr,
				FirstResponseEventID: firstEvID,
				FinalResponseEventID: finalEvID,
				RevisionCount:        it.RevisionCount,
				GradingVersion:       "grading-v1",
				IsCorrect:            it.IsCorrect,
				ObservedAt:           observedAt,
				CreatedAt:            nowMicro,
			}

			var instOpts []learning.InstanceOption
			for _, o := range it.Snapshot.Options {
				instOpts = append(instOpts, learning.InstanceOption{
					ID:               o.ID,
					OptionIdentityID: o.OptionIdentityID,
					IsCorrect:        (o.ID == it.Snapshot.CorrectOptionID),
				})
			}

			if _, err := s.learningService.ApplyAttempt(tc, attempt, instOpts); err != nil {
				return "", fmt.Errorf("apply exam attempt to learning: %w", err)
			}
		}
	}

	// Update session to submitted
	updateSess := `UPDATE session SET status = 'submitted', updated_at = ? WHERE id = ?`
	if _, err := tc.Exec(updateSess, nowMicro, examID); err != nil {
		return "", fmt.Errorf("update session status to submitted: %w", err)
	}

	return assessmentID, nil
}

// AbandonExam handles POST /api/v1/exams/{id}/abandon.
func (s *Service) AbandonExam(ctx context.Context, examID string, req AbandonExamRequest) (*AbandonExamResponse, error) {
	var res *AbandonExamResponse

	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		if err := core.CheckMaintenance(tc); err != nil {
			return ErrMaintenance
		}

		var (
			status          string
			currentRevision int64
			deadlineAt      sql.NullInt64
		)
		querySess := `SELECT status, revision, deadline_at FROM session WHERE id = ? AND kind = 'exam'`
		err := tc.QueryRow(querySess, examID).Scan(&status, &currentRevision, &deadlineAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrExamNotFound
		}
		if err != nil {
			return err
		}

		if status != "open" {
			return ErrSessionClosed
		}

		nowMicro := time.Now().UTC().UnixNano() / 1000

		// If deadline passed, auto-submit instead
		if deadlineAt.Valid && nowMicro >= deadlineAt.Int64 {
			_, err := s.submitExamInternal(tc, examID, "auto_deadline_abandon", true)
			if err != nil {
				return err
			}
			url := fmt.Sprintf("/exams/%s/result", examID)
			res = &AbandonExamResponse{
				Status:    "submitted",
				ResultURL: &url,
			}
			return nil
		}

		// Update to abandoned
		if req.ExpectedRevision != currentRevision {
			return &RevisionConflictNotAcceptedError{CurrentRevision: currentRevision}
		}
		if _, err := tc.Exec("UPDATE session SET status = 'abandoned', revision = revision + 1, updated_at = ? WHERE id = ?", nowMicro, examID); err != nil {
			return fmt.Errorf("update session to abandoned: %w", err)
		}

		res = &AbandonExamResponse{
			Status: "abandoned",
		}
		return nil
	})

	return res, err
}

// GetExamResult handles GET /api/v1/exams/{id}/result.
func (s *Service) GetExamResult(ctx context.Context, examID string) (*ExamResultResponse, error) {
	var (
		status string
	)
	err := s.db.QueryRowContext(ctx, "SELECT status FROM session WHERE id = ? AND kind = 'exam'", examID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrExamNotFound
	}
	if err != nil {
		return nil, err
	}
	if status != "submitted" {
		return nil, ErrExamNotSubmitted
	}

	var (
		assessID       string
		totalCount     int
		correctCount   int
		omittedCount   int
		voidCount      int
		ratioVal       sql.NullFloat64
		resultRevision int64
	)
	queryAssess := `
		SELECT id, total_count, correct_count, omitted_count, void_count, ratio, result_revision
		FROM assessment_result
		WHERE session_id = ?
	`
	err = s.db.QueryRowContext(ctx, queryAssess, examID).Scan(&assessID, &totalCount, &correctCount, &omittedCount, &voidCount, &ratioVal, &resultRevision)
	if err != nil {
		return nil, fmt.Errorf("query assessment_result: %w", err)
	}

	var ratioPtr *float64
	if ratioVal.Valid {
		ratioPtr = &ratioVal.Float64
	}

	queryItems := `
		SELECT air.instance_id, air.task_family, air.selected_option_id, air.correct_option_id,
		       air.is_correct, air.response_history, air.first_correct, qi.ordinal, qi.snapshot_json
		FROM assessment_item_result air
		JOIN question_instance qi ON qi.id = air.instance_id AND qi.session_id = ?
		WHERE air.assessment_id = ?
		ORDER BY qi.ordinal ASC
	`
	rows, err := s.db.QueryContext(ctx, queryItems, examID, assessID)
	if err != nil {
		return nil, fmt.Errorf("query assessment items: %w", err)
	}
	defer rows.Close()

	var questions []ExamResultQuestionItem
	analysis := ExamResponseAnalysis{}

	for rows.Next() {
		var (
			instID          string
			taskFamily      string
			selOptID        sql.NullString
			correctOptID    string
			isCorrectInt    int
			respHistory     string
			firstCorrectInt sql.NullInt64
			ordinal         int64
			snapJSON        string
		)
		if err := rows.Scan(&instID, &taskFamily, &selOptID, &correctOptID, &isCorrectInt, &respHistory, &firstCorrectInt, &ordinal, &snapJSON); err != nil {
			return nil, err
		}

		var snap InstanceSnapshot
		if err := json.Unmarshal([]byte(snapJSON), &snap); err != nil {
			return nil, fmt.Errorf("unmarshal instance snapshot for %s: %w", instID, err)
		}

		var resOpts []ResultOption
		for _, opt := range snap.Options {
			resOpts = append(resOpts, ResultOption{
				ID:   opt.ID,
				Text: opt.Text,
			})
		}

		var selOptPtr *string
		if selOptID.Valid {
			selOptPtr = &selOptID.String
		}

		var firstCorrPtr *bool
		if firstCorrectInt.Valid {
			b := (firstCorrectInt.Int64 == 1)
			firstCorrPtr = &b
			analysis.ReliableFirstChoices++
			if b {
				analysis.FirstChoiceCorrectCount++
			}
		}

		isCorr := (isCorrectInt == 1)
		if respHistory == "revised" {
			if isCorr {
				analysis.RevisedToCorrectCount++
			} else {
				analysis.RevisedToIncorrectCount++
			}
		}

		questions = append(questions, ExamResultQuestionItem{
			InstanceID:       instID,
			Ordinal:          ordinal,
			TaskFamily:       taskFamily,
			Stem:             snap.Stem,
			Options:          resOpts,
			SelectedOptionID: selOptPtr,
			CorrectOptionID:  correctOptID,
			IsCorrect:        isCorr,
			Explanation:      snap.Explanation,
			FirstCorrect:     firstCorrPtr,
			ResponseHistory:  respHistory,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate result questions: %w", err)
	}

	return &ExamResultResponse{
		ResultRevision:   resultRevision,
		Total:            totalCount,
		Correct:          correctCount,
		Omitted:          omittedCount,
		Void:             voidCount,
		Ratio:            ratioPtr,
		Questions:        questions,
		ResponseAnalysis: analysis,
	}, nil
}

// SettleExpiredExams settles all open exams whose deadline_at <= now.
func (s *Service) SettleExpiredExams(ctx context.Context) (int, error) {
	nowMicro := time.Now().UTC().UnixNano() / 1000
	query := `SELECT id FROM session WHERE kind = 'exam' AND status = 'open' AND deadline_at IS NOT NULL AND deadline_at <= ?`
	rows, err := s.db.QueryContext(ctx, query, nowMicro)
	if err != nil {
		return 0, fmt.Errorf("query expired exams: %w", err)
	}
	defer rows.Close()

	var expiredIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		expiredIDs = append(expiredIDs, id)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate expired exams: %w", err)
	}

	settled := 0
	var firstErr error
	for _, examID := range expiredIDs {
		err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
			_, err := s.submitExamInternal(tc, examID, "startup_settle", true)
			return err
		})
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("settle expired exam %s: %w", examID, err)
			}
		} else {
			settled++
		}
	}
	if firstErr != nil {
		return settled, firstErr
	}

	return settled, nil
}
