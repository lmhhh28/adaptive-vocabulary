package exam

import (
	"encoding/json"
	"fmt"
	"os"

	"adaptive-vocabulary/internal/content"
)

// ExamPolicyConfig holds exam settings loaded from policy.
type ExamPolicyConfig struct {
	MaxQuestions        int    `json:"max_questions"`
	MaxSearchStates     int    `json:"max_search_states"`
	RandomAlgorithm     string `json:"random_algorithm"`
	MaxAutomaticRebases int    `json:"max_automatic_rebases"`
}

func DefaultExamPolicyConfig() ExamPolicyConfig {
	return ExamPolicyConfig{
		MaxQuestions:        200,
		MaxSearchStates:     10000,
		RandomAlgorithm:     "math/rand/v2.PCG",
		MaxAutomaticRebases: 3,
	}
}

func LoadExamPolicyFromPath(path string) (*ExamPolicyConfig, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read policy file %q: %w", path, err)
	}

	var root struct {
		Exam ExamPolicyConfig `json:"exam"`
	}
	if err := json.Unmarshal(bytes, &root); err != nil {
		return nil, fmt.Errorf("unmarshal exam policy JSON: %w", err)
	}

	cfg := root.Exam
	if cfg.MaxQuestions <= 0 {
		cfg.MaxQuestions = 200
	}
	if cfg.MaxSearchStates <= 0 {
		cfg.MaxSearchStates = 10000
	}
	return &cfg, nil
}


// PublicOption represents an option presented to the user without answer keys.
type PublicOption struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// PublicQuestion represents a question instance without answer keys or internal attributes.
type PublicQuestion struct {
	InstanceID string         `json:"instance_id"`
	Ordinal    int64          `json:"ordinal"`
	TaskFamily string         `json:"task_family"`
	Stem       string         `json:"stem"`
	Options    []PublicOption `json:"options"`
}

// ExamDraftDTO represents the server's current draft for an instance.
type ExamDraftDTO struct {
	InstanceID       string  `json:"instance_id"`
	SelectedOptionID *string `json:"selected_option_id"`
	RevisionCount    int     `json:"revision_count"`
}

// CreateExamRequest is the body for POST /api/v1/exams.
type CreateExamRequest struct {
	BankID           string         `json:"bank_id"`
	QuestionCount    int            `json:"question_count"`
	TaskQuotas       map[string]int `json:"task_quotas,omitempty"`
	TimeLimitSeconds *int           `json:"time_limit_seconds,omitempty"`
	Tags             []string       `json:"tags,omitempty"`
}

// CreateExamResponse is the result of POST /api/v1/exams.
type CreateExamResponse struct {
	ExamID          string           `json:"exam_id"`
	SessionRevision int64            `json:"session_revision"`
	Deadline        *string          `json:"deadline,omitempty"`
	PublicQuestions []PublicQuestion `json:"public_questions"`
	Drafts          []ExamDraftDTO   `json:"drafts"`
	LastAckSequence int64            `json:"last_ack_sequence"`
}

// ExamDetailResponse is the result of GET /api/v1/exams/{id}.
type ExamDetailResponse struct {
	ID              string           `json:"id"`
	Status          string           `json:"status"`
	SessionRevision int64            `json:"session_revision"`
	Deadline        *string          `json:"deadline,omitempty"`
	PublicQuestions []PublicQuestion `json:"public_questions"`
	Drafts          []ExamDraftDTO   `json:"drafts"`
	LastAckSequence int64            `json:"last_ack_sequence"`
	ResultURL       *string          `json:"result_url,omitempty"`
}

// RebasedFromDTO captures the prior client sequence and revision when rebased.
type RebasedFromDTO struct {
	ClientSequence   int64 `json:"client_sequence"`
	ExpectedRevision int64 `json:"expected_revision"`
}

// ResponseEventRequest is the body for POST /api/v1/exams/{id}/response-events.
type ResponseEventRequest struct {
	InstanceID       string          `json:"instance_id"`
	Action           string          `json:"action"` // "select" or "clear"
	OptionID         *string         `json:"option_id,omitempty"`
	ClientEventID    string          `json:"client_event_id"`
	ClientSequence   int64           `json:"client_sequence"`
	ExpectedRevision int64           `json:"expected_revision"`
	RebasedFrom      *RebasedFromDTO `json:"rebased_from,omitempty"`
}

// ResponseEventResponse is the result of POST /api/v1/exams/{id}/response-events.
type ResponseEventResponse struct {
	EventID          string       `json:"event_id"`
	AcceptedSequence int64        `json:"accepted_sequence"`
	CurrentRevision  int64        `json:"current_revision"`
	Draft            ExamDraftDTO `json:"draft"`
}

// EventReceiptResponse is the result of GET /api/v1/exams/{id}/response-events/{client_event_id}.
type EventReceiptResponse struct {
	EventID          string  `json:"event_id"`
	ClientEventID    string  `json:"client_event_id"`
	ClientSequence   int64   `json:"client_sequence"`
	AcceptedRevision int64   `json:"accepted_revision"`
	InstanceID       string  `json:"instance_id"`
	Action           string  `json:"action"`
	OptionID         *string `json:"option_id,omitempty"`
}

// TelemetryGapsRequest is the body for POST /api/v1/exams/{id}/telemetry-gaps.
type TelemetryGapsRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"` // "lost_queue", "recovered_without_queue", "client_gap"
}

// TelemetryGapsResponse is the result of POST /api/v1/exams/{id}/telemetry-gaps.
type TelemetryGapsResponse struct {
	TelemetryIncomplete bool  `json:"telemetry_incomplete"`
	CurrentRevision     int64 `json:"current_revision"`
	LastAckSequence     int64 `json:"last_ack_sequence"`
}

// SubmitExamRequest is the body for POST /api/v1/exams/{id}/submit.
type SubmitExamRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	SubmissionID     string `json:"submission_id"`
	LastAckSequence  int64  `json:"last_ack_sequence"`
}

// SubmitExamResponse is the result of POST /api/v1/exams/{id}/submit.
type SubmitExamResponse struct {
	AssessmentID   string `json:"assessment_id"`
	Status         string `json:"status"`
	ResultRevision int64  `json:"result_revision"`
	ResultURL      string `json:"result_url"`
}

// AbandonExamRequest is the body for POST /api/v1/exams/{id}/abandon.
type AbandonExamRequest struct {
	ExpectedRevision int64 `json:"expected_revision"`
}

// AbandonExamResponse is the result of POST /api/v1/exams/{id}/abandon.
type AbandonExamResponse struct {
	Status    string  `json:"status"`
	ResultURL *string `json:"result_url,omitempty"`
}

// ResultOption represents an option displayed on the result report.
type ResultOption struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// ExamResultQuestionItem represents the feedback on a single question in the exam result.
type ExamResultQuestionItem struct {
	InstanceID       string         `json:"instance_id"`
	Ordinal          int64          `json:"ordinal"`
	TaskFamily       string         `json:"task_family"`
	Stem             string         `json:"stem"`
	Options          []ResultOption `json:"options"`
	SelectedOptionID *string        `json:"selected_option_id"`
	CorrectOptionID  string         `json:"correct_option_id"`
	IsCorrect        bool           `json:"is_correct"`
	Explanation      string         `json:"explanation"`
	FirstCorrect     *bool          `json:"first_correct"`
	ResponseHistory  string         `json:"response_history"`
}

// ExamResponseAnalysis summarizes student choices.
type ExamResponseAnalysis struct {
	FirstChoiceCorrectCount int `json:"first_choice_correct_count"`
	RevisedToCorrectCount   int `json:"revised_to_correct_count"`
	RevisedToIncorrectCount int `json:"revised_to_incorrect_count"`
	ReliableFirstChoices    int `json:"reliable_first_choices"`
}

// ExamResultResponse is the result of GET /api/v1/exams/{id}/result.
type ExamResultResponse struct {
	ResultRevision   int64                    `json:"result_revision"`
	Total            int                      `json:"total"`
	Correct          int                      `json:"correct"`
	Omitted          int                      `json:"omitted"`
	Void             int                      `json:"void"`
	Ratio            *float64                 `json:"ratio"`
	Questions        []ExamResultQuestionItem `json:"questions"`
	ResponseAnalysis ExamResponseAnalysis     `json:"response_analysis"`
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
