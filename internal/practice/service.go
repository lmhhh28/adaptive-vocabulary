package practice

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"adaptive-vocabulary/internal/content"
	"adaptive-vocabulary/internal/core"
	"adaptive-vocabulary/internal/learning"
	"adaptive-vocabulary/internal/selection"
)

var (
	ErrBankNotFound     = errors.New("bank not found")
	ErrBankArchived     = errors.New("bank is archived")
	ErrSessionNotFound  = errors.New("session not found")
	ErrSessionClosed    = errors.New("session is closed")
	ErrInstanceNotFound = errors.New("instance not found")
	ErrNotAnswered      = errors.New("question not answered")
	ErrInvalidOption    = errors.New("invalid option for this instance")
	ErrInvalidRequest   = errors.New("invalid request")
	ErrMaintenance      = errors.New("system under maintenance")
)

type ActiveSessionConflictError struct {
	ActiveSession *core.Session
}

func (e *ActiveSessionConflictError) Error() string {
	return fmt.Sprintf("active session exists with id %s (%s)", e.ActiveSession.ID, e.ActiveSession.Kind)
}

type AlreadyAnsweredError struct {
	SessionID string
	Cursor    int64
}

func (e *AlreadyAnsweredError) Error() string {
	return fmt.Sprintf("instance at cursor %d in session %s already answered", e.Cursor, e.SessionID)
}

type CursorConflictError struct {
	CurrentCursor int64
}

func (e *CursorConflictError) Error() string {
	return fmt.Sprintf("cursor conflict: current cursor is %d", e.CurrentCursor)
}

type RevisionConflictError struct {
	CurrentRevision int64
}

func (e *RevisionConflictError) Error() string {
	return fmt.Sprintf("revision conflict: current revision is %d", e.CurrentRevision)
}

type Service struct {
	db              *sql.DB
	learningService *learning.LearningService
	selector        *selection.Selector
}

func NewService(db *sql.DB, learningService *learning.LearningService, selector *selection.Selector) *Service {
	return &Service{
		db:              db,
		learningService: learningService,
		selector:        selector,
	}
}

// CreateOrResumeSession handles POST /api/v1/practice-sessions.
func (s *Service) CreateOrResumeSession(
	ctx context.Context,
	bankID string,
	idempotencyKey string,
	requestDigest string,
) (*core.Session, bool, error) {
	var (
		resumedSession *core.Session
		createdSession *core.Session
		isResumed      bool
	)

	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		// 1. Maintenance check
		if err := core.CheckMaintenance(tc); err != nil {
			return ErrMaintenance
		}

		// 2. Validate bank
		var bankArchived int
		queryBank := `SELECT archived FROM bank WHERE id = ?`
		err := tc.QueryRow(queryBank, bankID).Scan(&bankArchived)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBankNotFound
		}
		if err != nil {
			return fmt.Errorf("lookup bank: %w", err)
		}
		if bankArchived == 1 {
			return ErrBankArchived
		}

		// 3. Idempotency check
		if idempotencyKey != "" {
			rec, err := core.CheckIdempotency(tc, "POST", "/api/v1/practice-sessions", idempotencyKey, requestDigest)
			if errors.Is(err, core.ErrIdempotencyConflict) {
				return err
			}
			if err != nil {
				return fmt.Errorf("check idempotency: %w", err)
			}
			if rec != nil {
				var savedSess core.Session
				if err := json.Unmarshal([]byte(rec.ResponseBody), &savedSess); err == nil {
					resumedSession = &savedSess
					isResumed = true
					return nil
				}
			}
		}

		// 4. Check active session
		active, err := core.GetActiveSession(tc)
		if err != nil {
			return fmt.Errorf("check active session: %w", err)
		}
		if active != nil {
			if active.Kind == "practice" && active.BankID.Valid && active.BankID.String == bankID {
				resumedSession = active
				isResumed = true
				return nil
			}
			return &ActiveSessionConflictError{ActiveSession: active}
		}

		// 5. Create new practice session
		now := time.Now().UTC().UnixNano() / 1000
		sessionID := core.NewUUID()
		queryInsertSession := `
			INSERT INTO session (id, kind, status, bank_id, cursor, normal_position, revision, last_ack_sequence, deadline_at, created_at, updated_at)
			VALUES (?, 'practice', 'open', ?, 0, 1, 0, 0, NULL, ?, ?)
		`
		if _, err := tc.Exec(queryInsertSession, sessionID, bankID, now, now); err != nil {
			return fmt.Errorf("insert session: %w", err)
		}

		queryInsertPractice := `
			INSERT INTO practice_session (session_id, bank_id, unresolved_unit_count, created_at, updated_at)
			VALUES (?, ?, 0, ?, ?)
		`
		if _, err := tc.Exec(queryInsertPractice, sessionID, bankID, now, now); err != nil {
			return fmt.Errorf("insert practice_session: %w", err)
		}

		createdSession = &core.Session{
			ID:             sessionID,
			Kind:           "practice",
			Status:         "open",
			BankID:         sql.NullString{String: bankID, Valid: true},
			Cursor:         0,
			NormalPosition: sql.NullInt64{Int64: 1, Valid: true},
			Revision:       0,
			CreatedAt:      now,
			UpdatedAt:      now,
		}

		// Save idempotency record
		if idempotencyKey != "" {
			respBytes, _ := json.Marshal(createdSession)
			idempRec := &core.IdempotencyRecord{
				Operation:     "POST",
				ResourceScope: "/api/v1/practice-sessions",
				Key:           idempotencyKey,
				RequestDigest: requestDigest,
				Status:        "committed",
				ResponseCode:  201,
				ResponseBody:  string(respBytes),
				CreatedAt:     now,
			}
			if err := core.SaveIdempotencyRecord(tc, idempRec); err != nil {
				return fmt.Errorf("save idempotency: %w", err)
			}
		}

		return nil
	})

	if err != nil {
		return nil, false, err
	}
	if isResumed {
		return resumedSession, true, nil
	}
	return createdSession, false, nil
}

// GetSessionDetail handles GET /api/v1/practice-sessions/{id}.
func (s *Service) GetSessionDetail(ctx context.Context, sessionID string) (*PracticeSessionDetail, error) {
	querySession := `
		SELECT id, kind, status, bank_id, cursor, normal_position, revision, last_ack_sequence, deadline_at, created_at, updated_at
		FROM session
		WHERE id = ?
	`
	var sess core.Session
	err := s.db.QueryRowContext(ctx, querySession, sessionID).Scan(
		&sess.ID,
		&sess.Kind,
		&sess.Status,
		&sess.BankID,
		&sess.Cursor,
		&sess.NormalPosition,
		&sess.Revision,
		&sess.LastAckSequence,
		&sess.DeadlineAt,
		&sess.CreatedAt,
		&sess.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query session: %w", err)
	}

	detail := &PracticeSessionDetail{
		Session:       &sess,
		RecentRecords: []AnswerRecord{},
		Summary: PracticeCountsSummary{
			CountsScope:    "accepted_attempts",
			AnsweredCount:  0,
			CorrectCount:   0,
			IncorrectCount: 0,
		},
	}

	// 1. Calculate Summary
	querySummary := `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN is_correct = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN is_correct = 0 THEN 1 ELSE 0 END), 0)
		FROM attempt_event
		WHERE session_id = ?
	`
	_ = s.db.QueryRowContext(ctx, querySummary, sessionID).Scan(
		&detail.Summary.AnsweredCount,
		&detail.Summary.CorrectCount,
		&detail.Summary.IncorrectCount,
	)

	// 2. Fetch RecentRecords (up to 30 items, ascending by ordinal)
	queryRecent := `
		SELECT qi.id, qi.ordinal, ae.is_correct
		FROM attempt_event ae
		JOIN question_instance qi ON ae.instance_id = qi.id
		WHERE ae.session_id = ?
		ORDER BY qi.ordinal DESC
		LIMIT 30
	`
	rows, err := s.db.QueryContext(ctx, queryRecent, sessionID)
	if err == nil {
		defer rows.Close()
		var descRecords []AnswerRecord
		for rows.Next() {
			var r AnswerRecord
			var isCorr int
			if err := rows.Scan(&r.InstanceID, &r.Ordinal, &isCorr); err == nil {
				r.Correct = isCorr == 1
				descRecords = append(descRecords, r)
			}
		}
		// Reverse to ascending order
		n := len(descRecords)
		ascRecords := make([]AnswerRecord, n)
		for i, rec := range descRecords {
			ascRecords[n-1-i] = rec
		}
		detail.RecentRecords = ascRecords
	}
	detail.HasOlderRecords = detail.Summary.AnsweredCount > len(detail.RecentRecords)

	// 3. Check question instance at current cursor
	queryInst := `
		SELECT qi.id, qi.ordinal, qi.normal_position, qi.snapshot_json
		FROM question_instance qi
		WHERE qi.session_id = ? AND qi.ordinal = ?
	`
	var (
		instID      string
		ordinal     int64
		normalPos   sql.NullInt64
		snapshotStr string
	)
	err = s.db.QueryRowContext(ctx, queryInst, sessionID, sess.Cursor).Scan(&instID, &ordinal, &normalPos, &snapshotStr)
	hasUnansweredCurrent := false
	if err == nil {
		var dummyID string
		attErr := s.db.QueryRowContext(ctx, `SELECT id FROM attempt_event WHERE instance_id = ?`, instID).Scan(&dummyID)
		if errors.Is(attErr, sql.ErrNoRows) {
			// Unanswered instance -> populate CurrentQuestion
			var snap InstanceSnapshot
			if err := json.Unmarshal([]byte(snapshotStr), &snap); err == nil {
				publicOpts := make([]PublicOption, len(snap.Options))
				for i, opt := range snap.Options {
					publicOpts[i] = PublicOption{
						ID:   opt.ID,
						Text: opt.Text,
					}
				}
				detail.CurrentQuestion = &PublicQuestion{
					InstanceID: instID,
					Ordinal:    ordinal,
					TaskFamily: snap.TaskFamily,
					Stem:       snap.Stem,
					Options:    publicOpts,
				}
				hasUnansweredCurrent = true
			}
		}
	}

	// 4. Populate LastAnsweredQuestion and LastFeedback if no unanswered current question
	if !hasUnansweredCurrent && detail.Summary.AnsweredCount > 0 {
		queryLastAnswered := `
			SELECT qi.id, qi.ordinal, qi.snapshot_json, ae.id, ae.selected_option_id, ae.is_correct
			FROM attempt_event ae
			JOIN question_instance qi ON ae.instance_id = qi.id
			WHERE ae.session_id = ?
			ORDER BY qi.ordinal DESC
			LIMIT 1
		`
		var (
			lastInstID    string
			lastOrdinal   int64
			lastSnapStr   string
			lastAttID     string
			lastSelOptID  sql.NullString
			lastIsCorrect int
		)
		err := s.db.QueryRowContext(ctx, queryLastAnswered, sessionID).Scan(
			&lastInstID,
			&lastOrdinal,
			&lastSnapStr,
			&lastAttID,
			&lastSelOptID,
			&lastIsCorrect,
		)
		if err == nil {
			var snap InstanceSnapshot
			if err := json.Unmarshal([]byte(lastSnapStr), &snap); err == nil {
				publicOpts := make([]PublicOption, len(snap.Options))
				for i, opt := range snap.Options {
					publicOpts[i] = PublicOption{
						ID:   opt.ID,
						Text: opt.Text,
					}
				}
				q := PublicQuestion{
					InstanceID: lastInstID,
					Ordinal:    lastOrdinal,
					TaskFamily: snap.TaskFamily,
					Stem:       snap.Stem,
					Options:    publicOpts,
				}
				fb := AttemptFeedback{
					AttemptID:       lastAttID,
					Correct:         lastIsCorrect == 1,
					CorrectOptionID: snap.CorrectOptionID,
					Explanation:     snap.Explanation,
					NextCursor:      lastOrdinal + 1,
					SessionRevision: sess.Revision,
				}
				detail.LastAnsweredQuestion = &AcceptedAnswer{
					Question:         q,
					SelectedOptionID: lastSelOptID.String,
					Feedback:         fb,
				}
				detail.LastFeedback = &fb
			}
		}
	}

	return detail, nil
}

// GetAnswerRecords handles GET /api/v1/practice-sessions/{id}/answer-records.
func (s *Service) GetAnswerRecords(
	ctx context.Context,
	sessionID string,
	beforeOrdinal *int64,
	limit int,
) (*AnswerRecordsResult, error) {
	if limit <= 0 {
		limit = 30
	} else if limit > 100 {
		limit = 100
	}

	// 1. Verify session exists
	var sessRevision int64
	err := s.db.QueryRowContext(ctx, `SELECT revision FROM session WHERE id = ?`, sessionID).Scan(&sessRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query session revision: %w", err)
	}

	// 2. Summary
	var summary PracticeCountsSummary
	summary.CountsScope = "accepted_attempts"
	querySummary := `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN is_correct = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN is_correct = 0 THEN 1 ELSE 0 END), 0)
		FROM attempt_event
		WHERE session_id = ?
	`
	_ = s.db.QueryRowContext(ctx, querySummary, sessionID).Scan(
		&summary.AnsweredCount,
		&summary.CorrectCount,
		&summary.IncorrectCount,
	)

	// 3. Query items
	var (
		rows *sql.Rows
		qErr error
	)
	if beforeOrdinal != nil {
		queryItems := `
			SELECT qi.id, qi.ordinal, ae.is_correct
			FROM attempt_event ae
			JOIN question_instance qi ON ae.instance_id = qi.id
			WHERE ae.session_id = ? AND qi.ordinal < ?
			ORDER BY qi.ordinal DESC
			LIMIT ?
		`
		rows, qErr = s.db.QueryContext(ctx, queryItems, sessionID, *beforeOrdinal, limit)
	} else {
		queryItems := `
			SELECT qi.id, qi.ordinal, ae.is_correct
			FROM attempt_event ae
			JOIN question_instance qi ON ae.instance_id = qi.id
			WHERE ae.session_id = ?
			ORDER BY qi.ordinal DESC
			LIMIT ?
		`
		rows, qErr = s.db.QueryContext(ctx, queryItems, sessionID, limit)
	}
	if qErr != nil {
		return nil, fmt.Errorf("query answer records: %w", qErr)
	}
	defer rows.Close()

	var descRecords []AnswerRecord
	for rows.Next() {
		var r AnswerRecord
		var isCorr int
		if err := rows.Scan(&r.InstanceID, &r.Ordinal, &isCorr); err != nil {
			return nil, fmt.Errorf("scan answer record: %w", err)
		}
		r.Correct = isCorr == 1
		descRecords = append(descRecords, r)
	}

	// Reverse to ascending order
	n := len(descRecords)
	items := make([]AnswerRecord, n)
	for i, rec := range descRecords {
		items[n-1-i] = rec
	}

	result := &AnswerRecordsResult{
		Items:           items,
		Summary:         summary,
		SessionRevision: sessRevision,
	}

	// 4. Next before ordinal and has_more
	if len(items) > 0 {
		minOrdinal := items[0].Ordinal
		var hasOlder int
		checkErr := s.db.QueryRowContext(
			ctx,
			`SELECT 1 FROM attempt_event ae JOIN question_instance qi ON ae.instance_id = qi.id WHERE ae.session_id = ? AND qi.ordinal < ? LIMIT 1`,
			sessionID,
			minOrdinal,
		).Scan(&hasOlder)
		if checkErr == nil {
			result.HasMore = true
			result.NextBeforeOrdinal = &minOrdinal
		} else {
			result.HasMore = false
			result.NextBeforeOrdinal = nil
		}
	} else {
		result.HasMore = false
		result.NextBeforeOrdinal = nil
	}

	return result, nil
}

// GetAnsweredQuestion handles GET /api/v1/practice-sessions/{id}/answered-questions/{ordinal}.
func (s *Service) GetAnsweredQuestion(ctx context.Context, sessionID string, ordinal int64) (*AcceptedAnswer, error) {
	// 1. Check session existence
	var sessRevision int64
	err := s.db.QueryRowContext(ctx, `SELECT revision FROM session WHERE id = ?`, sessionID).Scan(&sessRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query session: %w", err)
	}

	// 2. Active session protection:
	// "若另有活动作答会话，禁止读取其他会话的答案正文；同一活动练习允许本轮已答回看。"
	active, err := core.GetActiveSession(s.db)
	if err != nil {
		return nil, fmt.Errorf("check active session: %w", err)
	}
	if active != nil && active.ID != sessionID {
		return nil, &ActiveSessionConflictError{ActiveSession: active}
	}

	// 3. Query answered question instance and attempt
	queryAnswered := `
		SELECT qi.id, qi.ordinal, qi.snapshot_json, ae.id, ae.selected_option_id, ae.is_correct
		FROM question_instance qi
		JOIN attempt_event ae ON ae.instance_id = qi.id
		WHERE qi.session_id = ? AND qi.ordinal = ?
	`
	var (
		instID      string
		ord         int64
		snapshotStr string
		attID       string
		selOptID    sql.NullString
		isCorrect   int
	)
	err = s.db.QueryRowContext(ctx, queryAnswered, sessionID, ordinal).Scan(
		&instID,
		&ord,
		&snapshotStr,
		&attID,
		&selOptID,
		&isCorrect,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotAnswered
	}
	if err != nil {
		return nil, fmt.Errorf("query answered question: %w", err)
	}

	var snap InstanceSnapshot
	if err := json.Unmarshal([]byte(snapshotStr), &snap); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}

	publicOpts := make([]PublicOption, len(snap.Options))
	for i, opt := range snap.Options {
		publicOpts[i] = PublicOption{
			ID:   opt.ID,
			Text: opt.Text,
		}
	}

	return &AcceptedAnswer{
		Question: PublicQuestion{
			InstanceID: instID,
			Ordinal:    ord,
			TaskFamily: snap.TaskFamily,
			Stem:       snap.Stem,
			Options:    publicOpts,
		},
		SelectedOptionID: selOptID.String,
		Feedback: AttemptFeedback{
			AttemptID:       attID,
			Correct:         isCorrect == 1,
			CorrectOptionID: snap.CorrectOptionID,
			Explanation:     snap.Explanation,
			NextCursor:      ord + 1,
			SessionRevision: sessRevision,
		},
	}, nil
}

// RecordFeedbackView handles POST /api/v1/practice-sessions/{id}/feedback-views.
func (s *Service) RecordFeedbackView(
	ctx context.Context,
	sessionID string,
	req FeedbackViewRequest,
	idempotencyKey string,
	requestDigest string,
) (*FeedbackViewResult, bool, error) {
	if req.Presentation != "history" && req.Presentation != "restored_feedback" {
		return nil, false, ErrInvalidRequest
	}
	if req.ClientEventID == "" || req.InstanceID == "" {
		return nil, false, ErrInvalidRequest
	}

	var (
		res        *FeedbackViewResult
		isReplayed bool
	)

	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		// 1. Maintenance check
		if err := core.CheckMaintenance(tc); err != nil {
			return ErrMaintenance
		}

		// 2. Idempotency check
		scope := fmt.Sprintf("/api/v1/practice-sessions/%s/feedback-views", sessionID)
		if idempotencyKey != "" {
			rec, err := core.CheckIdempotency(tc, "POST", scope, idempotencyKey, requestDigest)
			if errors.Is(err, core.ErrIdempotencyConflict) {
				return err
			}
			if err != nil {
				return fmt.Errorf("check idempotency: %w", err)
			}
			if rec != nil {
				isReplayed = true
				var cached FeedbackViewResult
				if err := json.Unmarshal([]byte(rec.ResponseBody), &cached); err == nil {
					res = &cached
					return nil
				}
			}
		}

		// 3. Verify session exists (even if ended, allowed!)
		var sessExists int
		err := tc.QueryRow(`SELECT 1 FROM session WHERE id = ?`, sessionID).Scan(&sessExists)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSessionNotFound
		}
		if err != nil {
			return fmt.Errorf("query session: %w", err)
		}

		// 4. Verify question instance belongs to session and has an accepted attempt
		queryInst := `
			SELECT qi.question_version_id, qv.sense_id, s.lexeme_id, qv.form_id
			FROM question_instance qi
			JOIN question_version qv ON qi.question_version_id = qv.id
			JOIN sense s ON qv.sense_id = s.id
			JOIN attempt_event ae ON ae.instance_id = qi.id
			WHERE qi.id = ? AND qi.session_id = ?
		`
		var (
			qvID     string
			senseID  string
			lexemeID string
			formID   string
		)
		err = tc.QueryRow(queryInst, req.InstanceID, sessionID).Scan(&qvID, &senseID, &lexemeID, &formID)
		if errors.Is(err, sql.ErrNoRows) {
			// Check if instance exists at all in this session
			var instExists int
			chkErr := tc.QueryRow(`SELECT 1 FROM question_instance WHERE id = ? AND session_id = ?`, req.InstanceID, sessionID).Scan(&instExists)
			if errors.Is(chkErr, sql.ErrNoRows) {
				return ErrInstanceNotFound
			}
			return ErrNotAnswered
		}
		if err != nil {
			return fmt.Errorf("verify instance attempt: %w", err)
		}

		// 5. Allocate global sequence
		now := time.Now().UTC()
		nowMicro := now.UnixNano() / 1000
		seqRes, err := tc.Exec("INSERT INTO global_sequence (event_name, created_at) VALUES ('practice_feedback_view', ?)", nowMicro)
		if err != nil {
			return fmt.Errorf("allocate global sequence: %w", err)
		}
		eventSeq, err := seqRes.LastInsertId()
		if err != nil {
			return fmt.Errorf("get sequence id: %w", err)
		}

		// 6. Insert practice_feedback_view
		viewID := core.NewUUID()
		recordedAtStr := now.Format(time.RFC3339)
		queryInsert := `
			INSERT INTO practice_feedback_view (
				id, session_id, instance_id, client_event_id, presentation,
				question_version_id, sense_id, lexeme_id, form_id,
				client_displayed_at, recorded_at, event_sequence, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`
		_, err = tc.Exec(
			queryInsert,
			viewID,
			sessionID,
			req.InstanceID,
			req.ClientEventID,
			req.Presentation,
			qvID,
			senseID,
			lexemeID,
			formID,
			req.ClientDisplayedAt,
			recordedAtStr,
			eventSeq,
			nowMicro,
		)
		if err != nil {
			return fmt.Errorf("insert feedback view: %w", err)
		}

		res = &FeedbackViewResult{
			ViewID:        viewID,
			ClientEventID: req.ClientEventID,
			EventSequence: eventSeq,
			RecordedAt:    recordedAtStr,
		}

		// 7. Save idempotency record
		if idempotencyKey != "" {
			respBytes, _ := json.Marshal(res)
			idempRec := &core.IdempotencyRecord{
				Operation:     "POST",
				ResourceScope: scope,
				Key:           idempotencyKey,
				RequestDigest: requestDigest,
				Status:        "committed",
				ResponseCode:  201,
				ResponseBody:  string(respBytes),
				CreatedAt:     nowMicro,
			}
			if err := core.SaveIdempotencyRecord(tc, idempRec); err != nil {
				return fmt.Errorf("save idempotency: %w", err)
			}
		}

		return nil
	})

	if err != nil {
		return nil, false, err
	}
	return res, isReplayed, nil
}

// NextQuestion handles POST /api/v1/practice-sessions/{id}/next.
func (s *Service) NextQuestion(
	ctx context.Context,
	sessionID string,
	expectedCursor int64,
) (*NextResult, error) {
	// 1. Initial checks outside transaction:
	// Verify session existence and basic state
	querySess := `
		SELECT id, kind, status, bank_id, cursor, normal_position, revision
		FROM session
		WHERE id = ?
	`
	var sess core.Session
	err := s.db.QueryRowContext(ctx, querySess, sessionID).Scan(
		&sess.ID,
		&sess.Kind,
		&sess.Status,
		&sess.BankID,
		&sess.Cursor,
		&sess.NormalPosition,
		&sess.Revision,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lookup session: %w", err)
	}
	if sess.Kind != "practice" {
		return nil, ErrInvalidRequest
	}
	if sess.Status != "open" {
		return nil, ErrSessionClosed
	}
	if sess.Cursor != expectedCursor {
		return nil, &CursorConflictError{CurrentCursor: sess.Cursor}
	}

	// 2. Check if unanswered instance already exists at (session_id, cursor)
	queryUnanswered := `
		SELECT qi.id, qi.ordinal, qi.snapshot_json
		FROM question_instance qi
		LEFT JOIN attempt_event a ON a.instance_id = qi.id
		WHERE qi.session_id = ? AND qi.ordinal = ? AND a.id IS NULL
	`
	var (
		existingInstID string
		existingOrd    int64
		existingSnap   string
	)
	err = s.db.QueryRowContext(ctx, queryUnanswered, sessionID, sess.Cursor).Scan(&existingInstID, &existingOrd, &existingSnap)
	if err == nil {
		// Found unanswered instance -> return it without re-generating
		var snap InstanceSnapshot
		if err := json.Unmarshal([]byte(existingSnap), &snap); err != nil {
			return nil, fmt.Errorf("unmarshal existing snapshot: %w", err)
		}
		publicOpts := make([]PublicOption, len(snap.Options))
		for i, opt := range snap.Options {
			publicOpts[i] = PublicOption{
				ID:   opt.ID,
				Text: opt.Text,
			}
		}
		return &NextResult{
			Status:          "ready",
			SessionRevision: sess.Revision,
			Cursor:          sess.Cursor,
			Question: &PublicQuestion{
				InstanceID: existingInstID,
				Ordinal:    existingOrd,
				TaskFamily: snap.TaskFamily,
				Stem:       snap.Stem,
				Options:    publicOpts,
			},
		}, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("check existing unanswered instance: %w", err)
	}

	// 3. Selection calculation (runs OUTSIDE write transaction to avoid DB pool deadlock)
	// per 00-frozen-design.md line 69: "训练发题计算可在事务外进行；保留事务再次检查活动状态..."
	normalPos := 1
	if sess.NormalPosition.Valid && sess.NormalPosition.Int64 > 0 {
		normalPos = int(sess.NormalPosition.Int64)
	}
	nowMicro := time.Now().UTC().UnixNano() / 1000

	pc, err := core.GetProjectionControl(s.db)
	if err != nil {
		return nil, fmt.Errorf("get projection control for selection: %w", err)
	}
	if pc.Mode == "rebuilding" {
		return nil, ErrMaintenance
	}
	activeVersion := pc.ActiveVersion
	if activeVersion == "" {
		activeVersion = "v1"
	}

	selRes, err := s.selector.SelectNextQuestion(ctx, selection.SelectionRequest{
		SessionID:         sessionID,
		BankID:            sess.BankID.String,
		NormalPosition:    normalPos,
		ProjectionVersion: activeVersion,
		NowMicro:          nowMicro,
	})
	if err != nil {
		return nil, fmt.Errorf("select next question: %w", err)
	}

	if selRes.Status == "unavailable" {
		return &NextResult{
			Status: "unavailable",
			Reason: selRes.Reason,
			Counts: map[string]int{
				"cooldown":        selRes.CooldownCount,
				"content_missing": selRes.ContentMissingCount,
				"exhausted":       selRes.ExhaustedCount,
			},
		}, nil
	}

	// 4. Reservation transaction: re-verify state and atomically persist question instance
	var result *NextResult
	err = core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		// Maintenance check and projection version consistency verification
		pcInTx, err := core.GetProjectionControl(tc)
		if err != nil {
			return fmt.Errorf("get projection control in reservation: %w", err)
		}
		if pcInTx.Mode == "rebuilding" {
			return ErrMaintenance
		}
		if pcInTx.ActiveVersion != activeVersion {
			return ErrMaintenance
		}

		// Re-verify session status, cursor, revision
		var (
			curStatus string
			curCursor int64
			curRev    int64
		)
		err = tc.QueryRow("SELECT status, cursor, revision FROM session WHERE id = ?", sessionID).Scan(&curStatus, &curCursor, &curRev)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSessionNotFound
		}
		if err != nil {
			return fmt.Errorf("re-verify session: %w", err)
		}
		if curStatus != "open" {
			return ErrSessionClosed
		}
		if curCursor != expectedCursor {
			return &CursorConflictError{CurrentCursor: curCursor}
		}

		// Check if another concurrent request created an unanswered instance
		var raceInstID string
		var raceOrd int64
		var raceSnap string
		err = tc.QueryRow(queryUnanswered, sessionID, curCursor).Scan(&raceInstID, &raceOrd, &raceSnap)
		if err == nil {
			var snap InstanceSnapshot
			if err := json.Unmarshal([]byte(raceSnap), &snap); err != nil {
				return fmt.Errorf("unmarshal race snapshot: %w", err)
			}
			publicOpts := make([]PublicOption, len(snap.Options))
			for i, opt := range snap.Options {
				publicOpts[i] = PublicOption{
					ID:   opt.ID,
					Text: opt.Text,
				}
			}
			result = &NextResult{
				Status:          "ready",
				SessionRevision: curRev,
				Cursor:          curCursor,
				Question: &PublicQuestion{
					InstanceID: raceInstID,
					Ordinal:    raceOrd,
					TaskFamily: snap.TaskFamily,
					Stem:       snap.Stem,
					Options:    publicOpts,
				},
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("re-check unanswered instance: %w", err)
		}

		// Verify bank status
		var bankArchived int
		if err := tc.QueryRow("SELECT archived FROM bank WHERE id = ?", sess.BankID.String).Scan(&bankArchived); err != nil {
			return fmt.Errorf("verify bank: %w", err)
		}
		if bankArchived == 1 {
			return ErrBankArchived
		}

		// Verify question version status
		var qvStatus string
		if err := tc.QueryRow("SELECT status FROM question_version WHERE id = ?", selRes.QuestionVersion.ID).Scan(&qvStatus); err != nil {
			return fmt.Errorf("verify question version: %w", err)
		}
		if qvStatus != "published" {
			return fmt.Errorf("question version %s is not published (status=%s)", selRes.QuestionVersion.ID, qvStatus)
		}

		// Determine purpose
		purpose := "normal"
		if selRes.IsCorrection {
			purpose = "correction"
		} else if selRes.CandidateGroup == "early_review" {
			purpose = "early_review"
		}

		// Create snapshot
		snap := InstanceSnapshot{
			Stem:            selRes.QuestionVersion.Stem,
			Options:         selRes.QuestionVersion.Options,
			CorrectOptionID: selRes.QuestionVersion.CorrectOptionID,
			Explanation:     selRes.QuestionVersion.Explanation,
			Purpose:         purpose,
			SenseID:         selRes.SenseID,
			LexemeID:        selRes.LexemeID,
			FormID:          selRes.FormID,
			TaskFamily:      selRes.TaskFamily,
		}
		snapBytes, err := json.Marshal(snap)
		if err != nil {
			return fmt.Errorf("marshal snapshot: %w", err)
		}

		// Insert question_instance
		newInstanceID := core.NewUUID()
		var normalPosVal any
		if !selRes.IsCorrection && sess.NormalPosition.Valid {
			normalPosVal = sess.NormalPosition.Int64
		}

		queryInsertInst := `
			INSERT INTO question_instance (id, session_id, ordinal, normal_position, question_version_id, snapshot_json, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`
		if _, err := tc.Exec(queryInsertInst, newInstanceID, sessionID, curCursor, normalPosVal, selRes.QuestionVersion.ID, string(snapBytes), nowMicro); err != nil {
			return fmt.Errorf("insert question_instance: %w", err)
		}

		// If normal/early_review, increment normal_position
		if !selRes.IsCorrection {
			queryIncPos := `UPDATE session SET normal_position = normal_position + 1, updated_at = ? WHERE id = ?`
			if _, err := tc.Exec(queryIncPos, nowMicro, sessionID); err != nil {
				return fmt.Errorf("increment session normal_position: %w", err)
			}
		}

		// Record reserved exposure
		exp := &learning.ExposureEvent{
			SourceInstanceID: newInstanceID,
			Kind:             learning.ExposureReserved,
			SenseID:          selRes.SenseID,
			LexemeID:         selRes.LexemeID,
			FormID:           selRes.FormID,
			ObservedAt:       nowMicro,
		}
		if err := learning.RecordExposure(tc, exp); err != nil {
			return fmt.Errorf("record reserved exposure: %w", err)
		}

		// Construct public question (strictly omitting answers / internals)
		publicOpts := make([]PublicOption, len(selRes.QuestionVersion.Options))
		for i, opt := range selRes.QuestionVersion.Options {
			publicOpts[i] = PublicOption{
				ID:   opt.ID,
				Text: opt.Text,
			}
		}

		result = &NextResult{
			Status:          "ready",
			SessionRevision: curRev,
			Cursor:          curCursor,
			Question: &PublicQuestion{
				InstanceID: newInstanceID,
				Ordinal:    curCursor,
				TaskFamily: selRes.TaskFamily,
				Stem:       selRes.QuestionVersion.Stem,
				Options:    publicOpts,
			},
		}

		return nil
	})

	if err != nil {
		return nil, err
	}
	return result, nil
}

// SubmitAttempt handles POST /api/v1/practice-sessions/{id}/attempts.
func (s *Service) SubmitAttempt(
	ctx context.Context,
	sessionID string,
	req SubmitAttemptRequest,
	idempotencyKey string,
	requestDigest string,
	testFault string,
) (*AttemptFeedback, bool, error) {
	var (
		feedback   *AttemptFeedback
		isReplayed bool
	)

	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		// 1. Maintenance check
		if err := core.CheckMaintenance(tc); err != nil {
			return ErrMaintenance
		}

		// 2. Idempotency check
		scope := fmt.Sprintf("/api/v1/practice-sessions/%s/attempts", sessionID)
		if idempotencyKey != "" {
			rec, err := core.CheckIdempotency(tc, "POST", scope, idempotencyKey, requestDigest)
			if errors.Is(err, core.ErrIdempotencyConflict) {
				return err
			}
			if err != nil {
				return fmt.Errorf("check idempotency: %w", err)
			}
			if rec != nil {
				var savedFB AttemptFeedback
				if err := json.Unmarshal([]byte(rec.ResponseBody), &savedFB); err == nil {
					feedback = &savedFB
					isReplayed = true
					return nil
				}
			}
		}

		// 3. Load session
		querySess := `
			SELECT id, kind, status, bank_id, cursor, normal_position, revision
			FROM session
			WHERE id = ?
		`
		var sess core.Session
		err := tc.QueryRow(querySess, sessionID).Scan(
			&sess.ID,
			&sess.Kind,
			&sess.Status,
			&sess.BankID,
			&sess.Cursor,
			&sess.NormalPosition,
			&sess.Revision,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSessionNotFound
		}
		if err != nil {
			return fmt.Errorf("lookup session: %w", err)
		}
		if sess.Kind != "practice" {
			return ErrInvalidRequest
		}
		if sess.Status != "open" {
			return ErrSessionClosed
		}

		// 4. Load instance
		queryInst := `
			SELECT id, session_id, ordinal, normal_position, question_version_id, snapshot_json
			FROM question_instance
			WHERE id = ?
		`
		var (
			instID      string
			instSessID  string
			instOrdinal int64
			instNormPos sql.NullInt64
			qvID        string
			snapJSON    string
		)
		err = tc.QueryRow(queryInst, req.InstanceID).Scan(&instID, &instSessID, &instOrdinal, &instNormPos, &qvID, &snapJSON)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInstanceNotFound
		}
		if err != nil {
			return fmt.Errorf("lookup question instance: %w", err)
		}
		if instSessID != sessionID {
			return ErrInvalidRequest
		}

		var snap InstanceSnapshot
		if err := json.Unmarshal([]byte(snapJSON), &snap); err != nil {
			return fmt.Errorf("parse snapshot: %w", err)
		}

		// 5. Check if instance was already answered
		queryExistingAtt := `SELECT id, selected_option_id, is_correct FROM attempt_event WHERE instance_id = ?`
		var (
			existingAttID string
			existingOptID sql.NullString
			existingCorr  int
		)
		err = tc.QueryRow(queryExistingAtt, req.InstanceID).Scan(&existingAttID, &existingOptID, &existingCorr)
		if err == nil {
			// Already answered!
			if existingOptID.Valid && existingOptID.String == req.OptionID {
				// Same option repeated -> return original feedback
				feedback = &AttemptFeedback{
					AttemptID:       existingAttID,
					Correct:         existingCorr == 1,
					CorrectOptionID: snap.CorrectOptionID,
					Explanation:     snap.Explanation,
					NextCursor:      instOrdinal + 1,
					SessionRevision: sess.Revision,
				}
				return nil
			}
			// Different option submitted -> conflict!
			return &AlreadyAnsweredError{SessionID: sessionID, Cursor: sess.Cursor}
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("check existing attempt: %w", err)
		}

		// Check cursor alignment
		if instOrdinal != sess.Cursor {
			return &CursorConflictError{CurrentCursor: sess.Cursor}
		}

		// 6. Validate option in snapshot
		var matchedOpt *content.QuestionOption
		for _, opt := range snap.Options {
			if opt.ID == req.OptionID {
				matchedOpt = &opt
				break
			}
		}
		if matchedOpt == nil {
			return ErrInvalidOption
		}

		isCorrect := (req.OptionID == snap.CorrectOptionID)
		nowMicro := time.Now().UTC().UnixNano() / 1000

		// 7. Construct AttemptEvent
		attemptID := req.SubmissionID
		if attemptID == "" {
			attemptID = core.NewUUID()
		}

		att := &learning.AttemptEvent{
			ID:               attemptID,
			InstanceID:       req.InstanceID,
			SessionID:        sessionID,
			SenseID:          snap.SenseID,
			TaskFamily:       snap.TaskFamily,
			Source:           learning.SourcePractice,
			Purpose:          snap.Purpose,
			ResponseHistory:  learning.ResponseSingleResponse,
			SelectedOptionID: &req.OptionID,
			FinalOptionID:    &req.OptionID,
			GradingVersion:   "grading-v1",
			IsCorrect:        isCorrect,
			ObservedAt:       nowMicro,
			CreatedAt:        nowMicro,
		}
		if req.Telemetry != nil {
			att.ClientActiveDurationMs = req.Telemetry.ClientActiveDurationMs
			att.InterruptionFlags = req.Telemetry.InterruptionFlags
		}

		// Convert options for learning service
		instOptions := make([]learning.InstanceOption, len(snap.Options))
		for i, opt := range snap.Options {
			instOptions[i] = learning.InstanceOption{
				ID:               opt.ID,
				OptionIdentityID: opt.OptionIdentityID,
				IsCorrect:        opt.ID == snap.CorrectOptionID,
			}
		}

		// 8. Apply attempt in learning service
		if _, err := s.learningService.ApplyAttempt(tc, att, instOptions); err != nil {
			return fmt.Errorf("apply attempt: %w", err)
		}

		// 9. Record feedback_available exposure
		exp := &learning.ExposureEvent{
			SourceInstanceID: req.InstanceID,
			Kind:             learning.ExposureFeedbackAvailable,
			SenseID:          snap.SenseID,
			LexemeID:         snap.LexemeID,
			FormID:           snap.FormID,
			ObservedAt:       nowMicro,
		}
		if err := learning.RecordExposure(tc, exp); err != nil {
			return fmt.Errorf("record feedback exposure: %w", err)
		}

		// 10. Advance session cursor and revision
		newCursor := sess.Cursor + 1
		newRevision := sess.Revision + 1
		queryAdvance := `UPDATE session SET cursor = ?, revision = ?, updated_at = ? WHERE id = ?`
		if _, err := tc.Exec(queryAdvance, newCursor, newRevision, nowMicro, sessionID); err != nil {
			return fmt.Errorf("advance session: %w", err)
		}

		// 11. Test fault injection (PR-04)
		if core.IsE2EEnabled() && testFault == "fail_after_attempt" {
			return fmt.Errorf("injected test fault: fail_after_attempt")
		}

		// 12. Build feedback response
		feedback = &AttemptFeedback{
			AttemptID:       att.ID,
			Correct:         isCorrect,
			CorrectOptionID: snap.CorrectOptionID,
			Explanation:     snap.Explanation,
			NextCursor:      newCursor,
			SessionRevision: newRevision,
		}

		// 13. Store idempotency record
		if idempotencyKey != "" {
			respBytes, _ := json.Marshal(feedback)
			idempRec := &core.IdempotencyRecord{
				Operation:     "POST",
				ResourceScope: scope,
				Key:           idempotencyKey,
				RequestDigest: requestDigest,
				Status:        "committed",
				ResponseCode:  200,
				ResponseBody:  string(respBytes),
				CreatedAt:     nowMicro,
			}
			if err := core.SaveIdempotencyRecord(tc, idempRec); err != nil {
				return fmt.Errorf("save idempotency: %w", err)
			}
		}

		return nil
	})

	if err != nil {
		return nil, false, err
	}
	return feedback, isReplayed, nil
}

// EndSession handles POST /api/v1/practice-sessions/{id}/end.
func (s *Service) EndSession(
	ctx context.Context,
	sessionID string,
	expectedRevision *int64,
	reason string,
) (*EndSessionResult, error) {
	res, _, err := s.EndSessionIdempotent(ctx, sessionID, expectedRevision, reason, "", "", "")
	return res, err
}

func (s *Service) EndSessionIdempotent(
	ctx context.Context, sessionID string, expectedRevision *int64, reason, scope, key, digest string,
) (*EndSessionResult, bool, error) {
	if reason != "ended" && reason != "abandoned" {
		return nil, false, ErrInvalidRequest
	}

	var result *EndSessionResult
	replayed := false

	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		if key != "" {
			rec, err := core.CheckIdempotency(tc, "POST", scope, key, digest)
			if err != nil {
				return err
			}
			if rec != nil {
				if err := json.Unmarshal([]byte(rec.ResponseBody), &result); err != nil {
					return err
				}
				replayed = true
				return nil
			}
		}
		if err := core.CheckMaintenance(tc); err != nil {
			return ErrMaintenance
		}
		// 1. Load session
		querySess := `
			SELECT id, kind, status, bank_id, cursor, normal_position, revision, last_ack_sequence, deadline_at, created_at, updated_at
			FROM session
			WHERE id = ?
		`
		var sess core.Session
		err := tc.QueryRow(querySess, sessionID).Scan(
			&sess.ID,
			&sess.Kind,
			&sess.Status,
			&sess.BankID,
			&sess.Cursor,
			&sess.NormalPosition,
			&sess.Revision,
			&sess.LastAckSequence,
			&sess.DeadlineAt,
			&sess.CreatedAt,
			&sess.UpdatedAt,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSessionNotFound
		}
		if err != nil {
			return fmt.Errorf("lookup session: %w", err)
		}
		if sess.Kind != "practice" {
			return ErrInvalidRequest
		}
		if sess.Status != "open" {
			return ErrSessionClosed
		}
		if expectedRevision != nil && sess.Revision != *expectedRevision {
			return &RevisionConflictError{CurrentRevision: sess.Revision}
		}

		// 2. Count unresolved units in correction_item
		var unresolvedCount int
		queryUnresolved := `
			SELECT count(*)
			FROM correction_item
			WHERE session_id = ? AND status = 'pending'
		`
		if err := tc.QueryRow(queryUnresolved, sessionID).Scan(&unresolvedCount); err != nil {
			return fmt.Errorf("count unresolved corrections: %w", err)
		}

		// 3. Update session
		nowMicro := time.Now().UTC().UnixNano() / 1000
		newRevision := sess.Revision + 1
		queryUpdateSess := `UPDATE session SET status = ?, revision = ?, updated_at = ? WHERE id = ?`
		if _, err := tc.Exec(queryUpdateSess, reason, newRevision, nowMicro, sessionID); err != nil {
			return fmt.Errorf("update session status: %w", err)
		}

		// 4. Update practice_session
		queryUpdatePrac := `UPDATE practice_session SET unresolved_unit_count = ?, updated_at = ? WHERE session_id = ?`
		if _, err := tc.Exec(queryUpdatePrac, unresolvedCount, nowMicro, sessionID); err != nil {
			return fmt.Errorf("update practice_session: %w", err)
		}

		sess.Status = reason
		sess.Revision = newRevision
		sess.UpdatedAt = nowMicro

		result = &EndSessionResult{
			Session:             &sess,
			UnresolvedUnitCount: unresolvedCount,
		}
		if key != "" {
			body, err := json.Marshal(result)
			if err != nil {
				return err
			}
			return core.SaveIdempotencyRecord(tc, &core.IdempotencyRecord{
				Operation: "POST", ResourceScope: scope, Key: key, RequestDigest: digest,
				Status: "completed", ResponseCode: 200, ResponseBody: string(body), CreatedAt: nowMicro,
			})
		}

		return nil
	})

	if err != nil {
		return nil, false, err
	}
	return result, replayed, nil
}
