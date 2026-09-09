package importer

type SourceEntryInput struct {
	ExternalEntryID string  `json:"external_entry_id"`
	RawWord         string  `json:"raw_word"`
	RawTranslation string  `json:"raw_translation"`
	POS             *string `json:"pos,omitempty"`
	Tags            *string `json:"tags,omitempty"`
	Aliases         *string `json:"aliases,omitempty"`
	Statistics      *string `json:"statistics,omitempty"`
}

type SourceImportRequest struct {
	SourceVersion    string             `json:"source_version"`
	Entries          []SourceEntryInput `json:"entries"`
	ExpectedRevision int64              `json:"expected_revision"`
}

type BankImportRequest struct {
	Words                   []string `json:"words,omitempty"`
	Text                    string   `json:"text,omitempty"`
	SourceRefs              []string `json:"source_refs,omitempty"`
	ResolutionPolicyVersion string   `json:"resolution_policy_version,omitempty"`
	ExpectedRevision        int64    `json:"expected_revision"`
}

type ResolveItemRequest struct {
	Action           string   `json:"action"` // "adopt" | "exclude"
	SenseRefs        []string `json:"sense_refs,omitempty"`
	Reason           string   `json:"reason,omitempty"`
	ExpectedRevision int64    `json:"expected_revision"`
}

type SenseCandidate struct {
	SenseID      string `json:"sense_id"`
	LexemeID     string `json:"lexeme_id"`
	PartOfSpeech string `json:"part_of_speech"`
	Meaning      string `json:"meaning"`
	SourceID     string `json:"source_id"`
}

type ImportJob struct {
	ID                      string `json:"id"`
	TargetType              string `json:"target_type"` // "source" | "bank"
	TargetID                string `json:"target_id"`
	Status                  string `json:"status"` // "pending" | "processing" | "completed" | "failed"
	SourceVersion           string `json:"source_version,omitempty"`
	ResolutionPolicyVersion string `json:"resolution_policy_version"`
	RawInputSummary         string `json:"raw_input_summary"`
	TotalCount              int    `json:"total_count"`
	ResolvedCount           int    `json:"resolved_count"`
	AmbiguousCount          int    `json:"ambiguous_count"`
	NotFoundCount           int    `json:"not_found_count"`
	InvalidCount            int    `json:"invalid_count"`
	ExcludedCount           int    `json:"excluded_count"`
	Revision                int64  `json:"revision"`
	CreatedAt               int64  `json:"created_at"`
	UpdatedAt               int64  `json:"updated_at"`
}

type ImportItem struct {
	ID                string           `json:"id"`
	ImportID          string           `json:"import_id"`
	BankID            *string          `json:"bank_id,omitempty"`
	Ordinal           int              `json:"ordinal"`
	RawInput          string           `json:"raw_input"`
	NormalizedWord    string           `json:"normalized_word"`
	SpecifiedSenseID  *string          `json:"specified_sense_id,omitempty"`
	Status            string           `json:"status"` // "resolved" | "ambiguous" | "not_found" | "invalid" | "excluded"
	Candidates        []SenseCandidate `json:"candidates"`
	ResolvedSenseIDs  []string         `json:"resolved_sense_ids"`
	DispositionReason string           `json:"disposition_reason,omitempty"`
	Revision          int64            `json:"revision"`
	CreatedAt         int64            `json:"created_at"`
	UpdatedAt         int64            `json:"updated_at"`
}

type ImportItemsResponse struct {
	Items  []ImportItem   `json:"items"`
	Total  int            `json:"total"`
	Counts map[string]int `json:"counts"`
}
