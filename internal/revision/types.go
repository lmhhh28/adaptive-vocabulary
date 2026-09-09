package revision

import "adaptive-vocabulary/internal/maintenance"

type EvidenceDispositionRequest struct {
	Action           string `json:"action"`
	Reason           string `json:"reason"`
	ExpectedRevision *int64 `json:"expected_revision,omitempty"`
}

type EvidenceDispositionResponse struct {
	DispositionID string `json:"disposition_id"`
	RebuildRunID  string `json:"rebuild_run_id"`
	Status        string `json:"status"`
}

type RetireRequest struct {
	Reason           string `json:"reason"`
	ExpectedRevision *int64 `json:"expected_revision,omitempty"`
}

type RetireResponse struct {
	VersionID string `json:"version_id"`
	Status    string `json:"status"`
	Revision  int64  `json:"revision"`
}

type SenseLearningDetail struct {
	SenseID           string               `json:"sense_id"`
	ProjectionVersion string               `json:"projection_version"`
	Tasks             []TaskLearningDetail `json:"tasks"`
}

type TaskLearningDetail struct {
	TaskFamily string         `json:"task_family"`
	Evidence   map[string]any `json:"evidence"`
	Memory     map[string]any `json:"memory"`
	Confusions []map[string]any `json:"confusions"`
}

type LearningSummaryResponse struct {
	CountsByTask       map[string]int `json:"counts_by_task"`
	EvidenceConditions map[string]any `json:"evidence_conditions"`
	ProjectionVersion  string         `json:"projection_version"`
}

type DispositionRecord struct {
	ID                string `json:"id"`
	QuestionVersionID string `json:"question_version_id"`
	Action            string `json:"action"`
	Reason            string `json:"reason"`
	RebuildRunID      string `json:"rebuild_run_id"`
	CreatedAt         int64  `json:"created_at"`
	Stem              string `json:"stem"`
	TaskFamily        string `json:"task_family"`
}

type RevisedReportItem struct {
	AssessmentID   string   `json:"assessment_id"`
	SessionID      string   `json:"session_id"`
	ResultRevision int64    `json:"result_revision"`
	TotalCount     int      `json:"total_count"`
	CorrectCount   int      `json:"correct_count"`
	OmittedCount   int      `json:"omitted_count"`
	VoidCount      int      `json:"void_count"`
	Ratio          *float64 `json:"ratio"`
	Reason         string   `json:"reason"`
	CreatedAt      int64    `json:"created_at"`
}

func (r RevisedReportItem) ValidCount() int {
	return r.TotalCount - r.VoidCount
}

type QualityPageData struct {
	Maintenance    *maintenance.MaintenanceStatus
	Dispositions   []DispositionRecord
	RebuildRuns    []maintenance.ModelRebuildRun
	RevisedReports []RevisedReportItem
	Questions      []QuestionSummary
}

type QuestionSummary struct {
	ID         string `json:"id"`
	Stem       string `json:"stem"`
	TaskFamily string `json:"task_family"`
	Status     string `json:"status"`
	Revision   int64  `json:"revision"`
}
