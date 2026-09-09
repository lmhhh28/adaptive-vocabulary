package maintenance

type MaintenanceStatus struct {
	Mode            string  `json:"mode"`
	RunID           *string `json:"run_id,omitempty"`
	RunStatus       *string `json:"run_status,omitempty"`
	ActiveVersion   string  `json:"active_version"`
	RequiresRebuild bool    `json:"requires_rebuild"`
	NextAction      *string `json:"next_action,omitempty"`
}

type ModelRebuildRun struct {
	ID                      string  `json:"id"`
	Kind                    string  `json:"kind"`
	BaseActiveVersion       string  `json:"base_active_version"`
	TargetProjectionVersion string  `json:"target_projection_version"`
	TargetPolicy            string  `json:"target_policy"`
	BaseRevisionDigest      string  `json:"base_revision_digest"`
	FixedWatermark          int64   `json:"fixed_watermark"`
	Status                  string  `json:"status"`
	RequiresRebuild         bool    `json:"requires_rebuild"`
	PredecessorRunID        *string `json:"predecessor_run_id,omitempty"`
	ErrorMessage            *string `json:"error_message,omitempty"`
	CreatedAt               int64   `json:"created_at"`
	UpdatedAt               int64   `json:"updated_at"`
}

type RebuildResult struct {
	RunID           string `json:"run_id"`
	Status          string `json:"status"`
	Mode            string `json:"mode"`
	ActiveVersion   string `json:"active_version"`
	RequiresRebuild bool   `json:"requires_rebuild"`
	NextAction      string `json:"next_action"`
}
