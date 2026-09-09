package backup

// Manifest represents the v1 backup manifest structure as defined in 00-frozen-design.md
type Manifest struct {
	FormatVersion     string           `json:"format_version"`
	SchemaVersion     int              `json:"schema_version"`
	AppVersion        string           `json:"app_version"`
	ContractVersion   string           `json:"contract_version"`
	PolicyVersion     string           `json:"policy_version"`
	ProjectionVersion string           `json:"projection_version"`
	SQLiteVersion     string           `json:"sqlite_version"`
	SQLiteSourceID    string           `json:"sqlite_source_id"`
	SnapshotWatermark int64            `json:"snapshot_watermark"`
	CreatedAt         int64            `json:"created_at"`
	CreatedAtISO      string           `json:"created_at_iso"`
	Files             []BackupFileInfo `json:"files"`
}

// BackupFileInfo represents an individual file archived in the backup
type BackupFileInfo struct {
	RelativePath string `json:"relative_path"`
	Role         string `json:"role"` // "database", "checkpoint"
	Bytes        int64  `json:"bytes"`
	SHA256       string `json:"sha256"`
}

// VerifyReport is the machine-readable result of avocab backup verify
type VerifyReport struct {
	OperationID    string            `json:"operation_id"`
	Status         string            `json:"status"` // "ok", "failed"
	InputManifest  string            `json:"input_manifest,omitempty"`
	EventWatermark int64             `json:"event_watermark"`
	Checks         map[string]string `json:"checks"`
	Errors         []string          `json:"errors,omitempty"`
}

// RestoreReport is the machine-readable result of avocab backup restore
type RestoreReport struct {
	OperationID     string               `json:"operation_id"`
	Status          string               `json:"status"` // "ok", "failed"
	TargetDirectory string               `json:"target_directory"`
	EventWatermark  int64                `json:"event_watermark"`
	Reconciliation  ReconciliationReport `json:"reconciliation"`
	Checks          map[string]string    `json:"checks"`
	Errors          []string             `json:"errors,omitempty"`
}

// ReconciliationReport captures state adjustments performed on the restored database
type ReconciliationReport struct {
	InvalidatedLeasesCount int            `json:"invalidated_leases_count"`
	UnknownCallsCount      int            `json:"unknown_calls_count"`
	CheckpointsReconciled  bool           `json:"checkpoints_reconciled"`
	Details                map[string]any `json:"details,omitempty"`
}
