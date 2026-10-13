package practice

import (
	"adaptive-vocabulary/internal/content"
	"adaptive-vocabulary/internal/core"
)

// CreateSessionRequest is the payload for POST /api/v1/practice-sessions.
type CreateSessionRequest struct {
	BankID string `json:"bank_id"`
}

// PublicOption represents an option presented to the user without any answer information.
type PublicOption struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// PublicQuestion represents a question instance without answer keys or internal quality attributes.
type PublicQuestion struct {
	InstanceID string         `json:"instance_id"`
	Ordinal    int64          `json:"ordinal"`
	TaskFamily string         `json:"task_family"`
	Stem       string         `json:"stem"`
	Options    []PublicOption `json:"options"`
}

// NextRequest is the payload for POST /api/v1/practice-sessions/{id}/next.
type NextRequest struct {
	ExpectedCursor *int64 `json:"expected_cursor"`
}

// NextResult is the response from POST /api/v1/practice-sessions/{id}/next.
type NextResult struct {
	Status          string          `json:"status"` // "ready" or "unavailable"
	SessionRevision int64           `json:"session_revision,omitempty"`
	Cursor          int64           `json:"cursor,omitempty"`
	Question        *PublicQuestion `json:"question,omitempty"`
	Reason          string          `json:"reason,omitempty"`
	Counts          map[string]int  `json:"counts,omitempty"`
}

// AttemptTelemetry represents optional telemetry reported with an attempt.
type AttemptTelemetry struct {
	ClientActiveDurationMs *int64   `json:"client_active_duration_ms,omitempty"`
	InterruptionFlags      []string `json:"interruption_flags,omitempty"`
	DisplayedAtClient      *int64   `json:"displayed_at_client,omitempty"`
	DisplayEventID         string   `json:"display_event_id,omitempty"`
}

// SubmitAttemptRequest is the payload for POST /api/v1/practice-sessions/{id}/attempts.
type SubmitAttemptRequest struct {
	InstanceID   string            `json:"instance_id"`
	OptionID     string            `json:"option_id"`
	SubmissionID string            `json:"submission_id,omitempty"`
	Telemetry    *AttemptTelemetry `json:"telemetry,omitempty"`
}

// AttemptFeedback is the response from POST /api/v1/practice-sessions/{id}/attempts.
type AttemptFeedback struct {
	AttemptID       string `json:"attempt_id"`
	Correct         bool   `json:"correct"`
	CorrectOptionID string `json:"correct_option_id"`
	Explanation     string `json:"explanation"`
	NextCursor      int64  `json:"next_cursor"`
	SessionRevision int64  `json:"session_revision"`
}

// EndSessionRequest is the payload for POST /api/v1/practice-sessions/{id}/end.
type EndSessionRequest struct {
	ExpectedRevision *int64 `json:"expected_revision"`
	Reason           string `json:"reason"` // "ended" or "abandoned"
}

// EndSessionResult is the response from POST /api/v1/practice-sessions/{id}/end.
type EndSessionResult struct {
	Session             *core.Session `json:"session"`
	UnresolvedUnitCount int           `json:"unresolved_unit_count"`
}

// AcceptedAnswer represents a past answered question along with user selection and feedback.
type AcceptedAnswer struct {
	Question         PublicQuestion  `json:"question"`
	SelectedOptionID string          `json:"selected_option_id"`
	Feedback         AttemptFeedback `json:"feedback"`
}

// AnswerRecord represents the lightweight answer status for answer sheet / pagination.
type AnswerRecord struct {
	InstanceID string `json:"instance_id"`
	Ordinal    int64  `json:"ordinal"` // 0-based
	Correct    bool   `json:"correct"`
}

// PracticeCountsSummary represents the accepted attempts counts summary.
type PracticeCountsSummary struct {
	CountsScope    string `json:"counts_scope"` // "accepted_attempts"
	AnsweredCount  int    `json:"answered_count"`
	CorrectCount   int    `json:"correct_count"`
	IncorrectCount int    `json:"incorrect_count"`
}

// AnswerRecordsResult is the response from GET /api/v1/practice-sessions/{id}/answer-records.
type AnswerRecordsResult struct {
	Items             []AnswerRecord        `json:"items"`
	NextBeforeOrdinal *int64                `json:"next_before_ordinal"`
	HasMore           bool                  `json:"has_more"`
	Summary           PracticeCountsSummary `json:"summary"`
	SessionRevision   int64                 `json:"session_revision"`
}

// FeedbackViewRequest is the payload for POST /api/v1/practice-sessions/{id}/feedback-views.
type FeedbackViewRequest struct {
	ClientEventID     string `json:"client_event_id"`
	InstanceID        string `json:"instance_id"`
	Presentation      string `json:"presentation"` // "history" or "restored_feedback"
	ClientDisplayedAt string `json:"client_displayed_at,omitempty"`
}

// FeedbackViewResult is the response from POST /api/v1/practice-sessions/{id}/feedback-views.
type FeedbackViewResult struct {
	ViewID        string `json:"view_id"`
	ClientEventID string `json:"client_event_id"`
	EventSequence int64  `json:"event_sequence"`
	RecordedAt    string `json:"recorded_at"`
}

// PracticeSessionDetail is the response from GET /api/v1/practice-sessions/{id}.
type PracticeSessionDetail struct {
	Session              *core.Session         `json:"session"`
	CurrentQuestion      *PublicQuestion       `json:"current_question,omitempty"`
	LastFeedback         *AttemptFeedback      `json:"last_feedback,omitempty"`
	LastAnsweredQuestion *AcceptedAnswer       `json:"last_answered_question,omitempty"`
	Summary              PracticeCountsSummary `json:"summary"`
	RecentRecords        []AnswerRecord        `json:"recent_records"`
	HasOlderRecords      bool                  `json:"has_older_records"`
}

// InstanceSnapshot represents the structured JSON saved in question_instance.snapshot_json.
type InstanceSnapshot struct {
	Stem            string                   `json:"stem"`
	Options         []content.QuestionOption `json:"options"`
	CorrectOptionID string                   `json:"correct_option_id"`
	Explanation     string                   `json:"explanation"`
	Purpose         string                   `json:"purpose"`
	SenseID         string                   `json:"sense_id"`
	LexemeID        string                   `json:"lexeme_id"`
	FormID          string                   `json:"form_id"`
	TaskFamily      string                   `json:"task_family"`
}
