package generation

import (
	"encoding/json"
	"errors"
)

var (
	ErrLeaseLost               = errors.New("lease expired or superseded by a newer fencing token")
	ErrBudgetExhausted         = errors.New("generation job budget limit reached")
	ErrCallAttemptsExhausted   = errors.New("maximum network attempts reached for logical call")
	ErrCallSettlementConflict  = errors.New("differing settlement digest for existing reservation settlement")
	ErrJobNotFound             = errors.New("generation job not found")
	ErrItemNotFound            = errors.New("generation item not found")
	ErrReservationNotFound     = errors.New("reservation not found")
	ErrCandidateNotFound       = errors.New("question candidate not found")
	ErrFindingNotFound         = errors.New("quality finding not found")
	ErrInvalidInputDigest      = errors.New("input digest does not match claimed item")
	ErrMaintenanceActive       = errors.New("system in maintenance mode")
	ErrSessionActive           = errors.New("session is currently active")
	ErrInvalidCandidateContent = errors.New("invalid candidate content")
)

type SenseRef struct {
	SenseID        string `json:"sense_id"`
	SenseVersionID string `json:"sense_version_id"`
}

type CreateJobRequest struct {
	SenseRefs               []SenseRef `json:"sense_refs"`
	GenerationConfigVersion string     `json:"generation_config_version"`
	QualityPolicyVersion    string     `json:"quality_policy_version"`
	ExpectedRevision        int        `json:"expected_revision"`
}

type CreateJobResponse struct {
	JobID            string `json:"job_id"`
	Status           string `json:"status"`
	FixedInputDigest string `json:"fixed_input_digest"`
}

type BudgetUsageSummary struct {
	AuthorizedRequests int  `json:"authorized_requests"`
	MaxRequests        int  `json:"max_requests"`
	TotalTokens        int  `json:"total_tokens"`
	MaxTokens          int  `json:"max_tokens"`
	BudgetOverrun      bool `json:"budget_overrun"`
}

type JobCandidateItem struct {
	ID                 string  `json:"id"`
	ItemID             string  `json:"item_id"`
	AttemptNumber      int     `json:"attempt_number"`
	CandidateIndex     int     `json:"candidate_index"`
	Disposition        string  `json:"disposition"`
	PublishedVersionID *string `json:"published_version_id,omitempty"`
	CreatedAt          int64   `json:"created_at"`
}

type JobSummary struct {
	ID                      string             `json:"id"`
	JobID                   string             `json:"job_id,omitempty"`
	BankID                  string             `json:"bank_id"`
	BankName                string             `json:"bank_name,omitempty"`
	Status                  string             `json:"status"`
	Requested               int                `json:"requested"`
	RequestedCount          int                `json:"requested_count,omitempty"`
	Published               int                `json:"published"`
	PublishedCount          int                `json:"published_count,omitempty"`
	Rejected                int                `json:"rejected"`
	RejectedCount           int                `json:"rejected_count,omitempty"`
	Pending                 int                `json:"pending"`
	PendingCount            int                `json:"pending_count,omitempty"`
	Revision                int                `json:"revision"`
	BudgetUsage             BudgetUsageSummary `json:"budget_usage"`
	FixedInputDigest        string             `json:"fixed_input_digest"`
	GenerationConfigVersion string             `json:"generation_config_version"`
	QualityPolicyVersion    string             `json:"quality_policy_version"`
	CreatedAt               int64              `json:"created_at"`
	UpdatedAt               int64              `json:"updated_at"`
	Candidates              []JobCandidateItem `json:"candidates,omitempty"`
}

type ListJobsResponse struct {
	Items      []JobSummary `json:"items"`
	NextCursor *string      `json:"next_cursor,omitempty"`
	HasMore    bool         `json:"has_more"`
}

type FixedItemInput struct {
	SenseID                 string   `json:"sense_id"`
	SenseVersionID          string   `json:"sense_version_id"`
	LexemeID                string   `json:"lexeme_id"`
	CanonicalForm           string   `json:"canonical_form"`
	PartOfSpeech            string   `json:"part_of_speech"`
	TargetMeaning           string   `json:"target_meaning"`
	SourceID                string   `json:"source_id"`
	SourceVersion           string   `json:"source_version"`
	CurriculumPolicyVersion string   `json:"curriculum_policy_version"`
	RequiredTaskFamilies    []string `json:"required_task_families"`
	RequestedFormCounts     int      `json:"requested_form_counts"`
	GenerationConfigVersion string   `json:"generation_config_version"`
	QualityPolicyVersion    string   `json:"quality_policy_version"`
}

type ClaimRequest struct {
	WorkerID     string   `json:"worker_id"`
	Capabilities []string `json:"capabilities,omitempty"`
	Limit        int      `json:"limit"`
}

type ClaimedItem struct {
	ItemID        string         `json:"item_id"`
	JobID         string         `json:"job_id"`
	FixedInput    FixedItemInput `json:"fixed_input"`
	InputDigest   string         `json:"input_digest"`
	LeaseUntil    int64          `json:"lease_until"`
	FencingToken  int64          `json:"fencing_token"`
	AttemptNumber int            `json:"attempt_number"`
}

type ClaimResponse struct {
	Items []ClaimedItem `json:"items"`
}

type RenewRequest struct {
	WorkerID     string `json:"worker_id"`
	ItemID       string `json:"item_id"`
	FencingToken int64  `json:"fencing_token"`
	InputDigest  string `json:"input_digest"`
}

type RenewResponse struct {
	LeaseUntil int64 `json:"lease_until"`
}

type LogicalCallKey struct {
	AttemptNumber   int    `json:"attempt_number"`
	Stage           string `json:"stage"` // "generator" | "evaluator"
	CandidateIndex  int    `json:"candidate_index"`
	EvaluationIndex int    `json:"evaluation_index,omitempty"`
}

func (k LogicalCallKey) String() string {
	b, _ := json.Marshal(k)
	return string(b)
}

type ReserveRequest struct {
	WorkerID           string         `json:"worker_id"`
	ItemID             string         `json:"item_id"`
	FencingToken       int64          `json:"fencing_token"`
	InputDigest        string         `json:"input_digest"`
	LogicalCallKey     LogicalCallKey `json:"logical_call_key"`
	NetworkAttemptID   string         `json:"network_attempt_id"`
	ProfileVersion     string         `json:"profile_version"`
	InputSHA256        string         `json:"input_sha256"`
	InputTokenEstimate int            `json:"input_token_estimate"`
	MaxOutputTokens    int            `json:"max_output_tokens"`
}

type BudgetRemaining struct {
	Requests int `json:"requests"`
	Tokens   int `json:"tokens"`
}

type ReserveResponse struct {
	ReservationID      string          `json:"reservation_id"`
	ReservedTokens     int             `json:"reserved_tokens"`
	AuthorizedRequests int             `json:"authorized_requests"`
	BudgetRemaining    BudgetRemaining `json:"budget_remaining"`
}

type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type SettleRequest struct {
	WorkerID          string      `json:"worker_id"`
	ItemID            string      `json:"item_id"`
	FencingToken      int64       `json:"fencing_token"`
	InputDigest       string      `json:"input_digest"`
	Outcome           string      `json:"outcome"` // "response" | "network_error" | "unknown"
	ProviderRequestID *string     `json:"provider_request_id,omitempty"`
	ResponseSHA256    *string     `json:"response_sha256,omitempty"`
	Usage             *TokenUsage `json:"usage,omitempty"`
}

type JobBudgetStatus struct {
	AuthorizedRequests int  `json:"authorized_requests"`
	TotalTokens        int  `json:"total_tokens"`
	BudgetOverrun      bool `json:"budget_overrun"`
}

type SettleResponse struct {
	CallStatus      string          `json:"call_status"` // "settled" | "unknown"
	AccountedTokens int             `json:"accounted_tokens"`
	JobBudgetStatus JobBudgetStatus `json:"job_budget_status"`
}

type CandidateOption struct {
	ID               string `json:"id,omitempty"`
	Text             string `json:"text"`
	IsCorrect        bool   `json:"is_correct"`
	Explanation      string `json:"explanation,omitempty"`
	Language         string `json:"language,omitempty"`
	OptionIdentityID string `json:"option_identity_id,omitempty"`
	SenseID          string `json:"sense_id,omitempty"`
}

type CandidateContent struct {
	TaskFamily  string            `json:"task_family"`
	Stem        string            `json:"stem"`
	Options     []CandidateOption `json:"options"`
	Explanation string            `json:"explanation,omitempty"`
}

type GenerationMetadata struct {
	GeneratorReservationID   string   `json:"generator_reservation_id"`
	EvaluatorReservationIDs []string `json:"evaluator_reservation_ids"`
}

type SemanticEvaluation struct {
	ProfileVersion string            `json:"profile_version"`
	Model          string            `json:"model"`
	PromptVersion  string            `json:"prompt_version"`
	Ratings        map[string]string `json:"ratings"` // e.g. target_meaning: pass
	Rationale      string            `json:"rationale"`
}

type CandidateChecks struct {
	RuleChecks          map[string]any       `json:"rule_checks,omitempty"`
	SemanticEvaluations []SemanticEvaluation `json:"semantic_evaluations"`
	Result              string               `json:"result"` // "pass" | "reject" | "uncertain"
}

type SubmitCandidateRequest struct {
	WorkerID           string             `json:"worker_id"`
	ItemID             string             `json:"item_id"`
	FencingToken       int64              `json:"fencing_token"`
	InputDigest        string             `json:"input_digest"`
	AttemptNumber      int                `json:"attempt_number"`
	CandidateIndex     int                `json:"candidate_index"`
	ParentCandidateID  *string            `json:"parent_candidate_id,omitempty"`
	Content            CandidateContent   `json:"content"`
	GenerationMetadata GenerationMetadata `json:"generation_metadata"`
	Checks             CandidateChecks    `json:"checks"`
}

type SubmitCandidateResponse struct {
	CandidateID        string  `json:"candidate_id"`
	Disposition        string  `json:"disposition"` // "published" | "auto_passed" | "rejected" | "uncertain" | "pending_maintenance"
	PublishedVersionID *string `json:"published_version_id,omitempty"`
}

type CompleteItemRequest struct {
	WorkerID     string  `json:"worker_id"`
	ItemID       string  `json:"item_id"`
	FencingToken int64   `json:"fencing_token"`
	InputDigest  string  `json:"input_digest"`
	Outcome      string  `json:"outcome"` // "completed" | "needs_attention" | "budget_exhausted" | "failed"
	Reason       *string `json:"reason,omitempty"`
}

type CompleteItemResponse struct {
	ItemStatus string `json:"item_status"`
	Revision   int    `json:"revision"`
}

type CandidateDetailResponse struct {
	Candidate   json.RawMessage `json:"candidate"`
	Checks      json.RawMessage `json:"checks"`
	Provenance  map[string]any  `json:"provenance"`
	Disposition string          `json:"disposition"`
	Revision    int             `json:"revision"`
}

type QualityFindingItem struct {
	ID             string          `json:"id"`
	JobID          string          `json:"job_id"`
	CandidateID    string          `json:"candidate_id"`
	FindingType    string          `json:"finding_type"`
	Details        json.RawMessage `json:"details"`
	CheckerVersion string          `json:"checker_version"`
	Status         string          `json:"status"`
	ResolutionID   *string         `json:"resolution_id,omitempty"`
	Revision       int             `json:"revision"`
	CreatedAt      int64           `json:"created_at"`
	UpdatedAt      int64           `json:"updated_at"`
}

type QualityFindingsResponse struct {
	Items      []QualityFindingItem `json:"items"`
	NextCursor *string              `json:"next_cursor,omitempty"`
	HasMore    bool                 `json:"has_more"`
}

type ResolveFindingRequest struct {
	Action           string            `json:"action"` // "revise" | "exclude" | "regenerate" | "restore_validity"
	Reason           string            `json:"reason"`
	Content          *CandidateContent `json:"content,omitempty"`
	ExpectedRevision int               `json:"expected_revision"`
}

type ResolveFindingResponse struct {
	ResolutionID string `json:"resolution_id"`
	Status       string `json:"status"`
	Revision     int    `json:"revision"`
}
