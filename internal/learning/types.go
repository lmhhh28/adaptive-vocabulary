package learning

// Task family constants
const (
	TaskMeaningRecognition = "meaning_recognition"
	TaskReverseSelection   = "reverse_selection"
	TaskContextSelection   = "context_selection"
)

// TaskFamilies is the ordered slice of supported task families
var TaskFamilies = []string{
	TaskMeaningRecognition,
	TaskReverseSelection,
	TaskContextSelection,
}

// Purpose constants
const (
	PurposeNormal      = "normal"
	PurposeCorrection  = "correction"
	PurposeEarlyReview = "early_review"
)

// Source constants
const (
	SourcePractice = "practice"
	SourceExam     = "exam"
)

// Response history constants
const (
	ResponseSingleResponse        = "single_response"
	ResponseRevised               = "revised"
	ResponseTelemetryIncomplete   = "telemetry_incomplete"
	ResponseOmitted               = "omitted"
)

// Exposure kinds
const (
	ExposureReserved             = "reserved"
	ExposureFeedbackAvailable    = "feedback_available"
	ExposureReportedDisplay      = "reported_display"
	ExposureSameFormReserved     = "same_form_reserved"
	ExposureRecentRelated        = "recent_related_exposure"
)

// Confusion stat groups
const (
	ConfusionStatGroupOrdinary   = "ordinary"
	ConfusionStatGroupCorrection = "correction"
	ConfusionStatGroupUncertain  = "uncertain"
)

// Correction item status
const (
	CorrectionStatusPending   = "pending"
	CorrectionStatusCompleted = "completed"
	CorrectionStatusExhausted = "exhausted"
	CorrectionStatusAbandoned = "abandoned"
)

// AttemptEvent represents a single submission in practice or an assessed question in exam
type AttemptEvent struct {
	ID                     string   `json:"id"`
	InstanceID             string   `json:"instance_id"`
	SessionID              string   `json:"session_id"`
	SenseID                string   `json:"sense_id"`
	TaskFamily             string   `json:"task_family"`
	Source                 string   `json:"source"`
	Purpose                string   `json:"purpose"`
	ResponseHistory        string   `json:"response_history"`
	SelectedOptionID       *string  `json:"selected_option_id"`
	FinalOptionID          *string  `json:"final_option_id"`
	FirstResponseEventID   *string  `json:"first_response_event_id"`
	FinalResponseEventID   *string  `json:"final_response_event_id"`
	RevisionCount          int      `json:"revision_count"`
	ClientActiveDurationMs *int64   `json:"client_active_duration_ms"`
	InterruptionFlags      []string `json:"interruption_flags"`
	GradingVersion         string   `json:"grading_version"`
	IsCorrect              bool     `json:"is_correct"`
	ObservedAt             int64    `json:"observed_at"` // microsecond epoch
	EventSequence          int64    `json:"event_sequence"`
	CreatedAt              int64    `json:"created_at"`
}

// ResponseEvent represents an in-exam user action (select/clear/noop)
type ResponseEvent struct {
	ID               string  `json:"id"`
	SessionID        string  `json:"session_id"`
	ClientEventID    string  `json:"client_event_id"`
	ClientSequence   int64   `json:"client_sequence"`
	InstanceID       string  `json:"instance_id"`
	Action           string  `json:"action"` // select, clear, noop
	OptionID         *string `json:"option_id"`
	ExpectedRevision int64   `json:"expected_revision"`
	AcceptedRevision int64   `json:"accepted_revision"`
	RebasedFrom      *string `json:"rebased_from"`
	ObservedAt       int64   `json:"observed_at"`
	EventSequence    int64   `json:"event_sequence"`
	CreatedAt        int64   `json:"created_at"`
}

// ExposureEvent represents an exposure to a form or related lexeme
type ExposureEvent struct {
	ID                string  `json:"id"`
	SourceInstanceID  string  `json:"source_instance_id"`
	Kind              string  `json:"kind"`
	SenseID           string  `json:"sense_id"`
	LexemeID          string  `json:"lexeme_id"`
	FormID            string  `json:"form_id"`
	ObservedAt        int64   `json:"observed_at"`
	EventSequence     int64   `json:"event_sequence"`
	ClientDisplayedAt *int64  `json:"client_displayed_at"`
	DisplayEventID    *string `json:"display_event_id"`
	CreatedAt         int64   `json:"created_at"`
}

// ReviewEvent stores a generated FSRS card transition
type ReviewEvent struct {
	ID                       string  `json:"id"`
	AttemptID                string  `json:"attempt_id"`
	SenseID                  string  `json:"sense_id"`
	TaskFamily               string  `json:"task_family"`
	ObservationPolicyVersion string  `json:"observation_policy_version"`
	Rating                   int     `json:"rating"` // 1=Again, 3=Good
	ReviewTime               int64   `json:"review_time"`
	FSRSState                int     `json:"fsrs_state"`
	FSRSStep                 int     `json:"fsrs_step"`
	Stability                float64 `json:"stability"`
	Difficulty               float64 `json:"difficulty"`
	DueAt                    int64   `json:"due_at"`
	Reps                     int     `json:"reps"`
	Lapses                   int     `json:"lapses"`
	ElapsedDays              float64 `json:"elapsed_days"`
	ScheduledDays            float64 `json:"scheduled_days"`
	FSRSVersion              string  `json:"fsrs_version"`
	CreatedAt                int64   `json:"created_at"`
}

// EventRevision represents an invalidation or disputed disposition on an event
type EventRevision struct {
	ID           string `json:"id"`
	EventID      string `json:"event_id"`
	EventType    string `json:"event_type"`
	RevisionKind string `json:"revision_kind"` // disputed, invalidate, restore_validity
	Reason       string `json:"reason"`
	CreatedAt    int64  `json:"created_at"`
}

// EvidenceProjection holds descriptive performance evidence for (sense_id, task_family)
type EvidenceProjection struct {
	ProjectionVersion                string `json:"projection_version"`
	SenseID                          string `json:"sense_id"`
	TaskFamily                       string `json:"task_family"`
	OrdinarySamples                  int    `json:"ordinary_samples"`
	OrdinaryCorrect                  int    `json:"ordinary_correct"`
	NormalSamples                    int    `json:"normal_samples"`
	NormalCorrect                    int    `json:"normal_correct"`
	EarlyReviewSamples               int    `json:"early_review_samples"`
	EarlyReviewCorrect               int    `json:"early_review_correct"`
	ExamSingleSamples                int    `json:"exam_single_samples"`
	ExamSingleCorrect                int    `json:"exam_single_correct"`
	CorrectionSamples                int    `json:"correction_samples"`
	CorrectionCorrect                int    `json:"correction_correct"`
	ExamRevisedSamples               int    `json:"exam_revised_samples"`
	ExamTelemetryIncompleteSamples   int    `json:"exam_telemetry_incomplete_samples"`
	OmittedSamples                   int    `json:"omitted_samples"`
	LastOrdinaryObservedAt           *int64 `json:"last_ordinary_observed_at"`
	LastOrdinaryCorrect              *bool  `json:"last_ordinary_correct"`
	EarliestOrdinaryObservedAt       *int64 `json:"earliest_ordinary_observed_at"`
	NeedsConsolidation               bool   `json:"needs_consolidation"`
	AppliedEventSequence             int64  `json:"applied_event_sequence"`
	UpdatedAt                        int64  `json:"updated_at"`
}

// MemoryProjection holds the materialized FSRS card state for (sense_id, task_family)
type MemoryProjection struct {
	ProjectionVersion    string  `json:"projection_version"`
	SenseID              string  `json:"sense_id"`
	TaskFamily           string  `json:"task_family"`
	FSRSState            int     `json:"fsrs_state"`
	FSRSStep             int     `json:"fsrs_step"`
	Stability            float64 `json:"stability"`
	Difficulty           float64 `json:"difficulty"`
	DueAt                int64   `json:"due_at"`
	LastReviewAt         *int64  `json:"last_review_at"`
	Reps                 int     `json:"reps"`
	Lapses               int     `json:"lapses"`
	ElapsedDays          float64 `json:"elapsed_days"`
	ScheduledDays        float64 `json:"scheduled_days"`
	AppliedEventSequence int64   `json:"applied_event_sequence"`
	UpdatedAt            int64   `json:"updated_at"`
}

// ConfusionProjection holds distractor exposure & error statistics
type ConfusionProjection struct {
	ProjectionVersion    string `json:"projection_version"`
	TargetSenseID        string `json:"target_sense_id"`
	TaskFamily           string `json:"task_family"`
	DistractorIdentityID string `json:"distractor_identity_id"`
	StatGroup            string `json:"stat_group"` // ordinary, correction, uncertain
	EligibleOfferedCount int    `json:"eligible_offered_count"`
	WrongSelectedCount   int    `json:"wrong_selected_count"`
	LastOfferedAt        *int64 `json:"last_offered_at"`
	LastSelectedAt       *int64 `json:"last_selected_at"`
	AppliedEventSequence int64  `json:"applied_event_sequence"`
	UpdatedAt            int64  `json:"updated_at"`
}

// CorrectionItem tracks the correction retry state for a unit in a practice session
type CorrectionItem struct {
	SessionID                 string `json:"session_id"`
	SenseID                   string `json:"sense_id"`
	TaskFamily                string `json:"task_family"`
	TriggeringAttemptID       string `json:"triggering_attempt_id"`
	TriggeringEventSequence   int64  `json:"triggering_event_sequence"`
	RetriesCount              int    `json:"retries_count"`
	Status                    string `json:"status"` // pending, completed, exhausted, abandoned
	CreatedAt                 int64  `json:"created_at"`
	UpdatedAt                 int64  `json:"updated_at"`
}

// InstanceOption models an option associated with a question instance
type InstanceOption struct {
	ID                 string `json:"id"`
	OptionIdentityID   string `json:"option_identity_id"`
	IsCorrect          bool   `json:"is_correct"`
}

// EligibilityDecision captures how an attempt is classified under policy-v2
type EligibilityDecision struct {
	IsValid             bool
	OrdinaryEvidence    bool
	OrdinaryConfusion   bool
	UpdateConsolidation bool
	NormalGap           bool
	CorrectionGap       bool
	FSRSReview          bool
}
