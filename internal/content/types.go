package content

type Source struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Location      string  `json:"location"`
	SourceVersion string  `json:"source_version"`
	License       string  `json:"license"`
	UsageScope    string  `json:"usage_scope"`
	Checksum      *string `json:"checksum,omitempty"`
	Revision      int64   `json:"revision"`
	CreatedAt     int64   `json:"created_at"`
	UpdatedAt     int64   `json:"updated_at"`
}

type Lexeme struct {
	ID            string `json:"id"`
	CanonicalForm string `json:"canonical_form"`
	CreatedAt     int64  `json:"created_at"`
}

type LexemeAlias struct {
	ID        string `json:"id"`
	Form      string `json:"form"`
	LexemeID  string `json:"lexeme_id"`
	CreatedAt int64  `json:"created_at"`
}

type Sense struct {
	ID           string   `json:"id"`
	LexemeID     string   `json:"lexeme_id"`
	PartOfSpeech string   `json:"part_of_speech"`
	SourceID     string   `json:"source_id"`
	CreatedAt    int64    `json:"created_at"`
}

type SenseVersion struct {
	ID            string   `json:"id"`
	SenseID       string   `json:"sense_id"`
	Meaning       string   `json:"meaning"`
	Frequency     *float64 `json:"frequency,omitempty"`
	VersionNumber int      `json:"version_number"`
	CreatedAt     int64    `json:"created_at"`
}

type OptionIdentity struct {
	ID             string  `json:"id"`
	CanonicalKey   string  `json:"canonical_key"`
	Language       string  `json:"language"`
	NormalizedText string  `json:"normalized_text"`
	SenseID        *string `json:"sense_id,omitempty"`
	SourceID       *string `json:"source_id,omitempty"`
	SourceVersion  *string `json:"source_version,omitempty"`
	CreatedAt      int64   `json:"created_at"`
}

type QuestionOption struct {
	ID               string `json:"id"`
	Text             string `json:"text"`
	SenseID          string `json:"sense_id"`
	Language         string `json:"language"`
	OptionIdentityID string `json:"option_identity_id"`
}

type PublicOption struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type QuestionForm struct {
	ID             string `json:"id"`
	SenseID        string `json:"sense_id"`
	TaskFamily     string `json:"task_family"`
	DedupHash      string `json:"dedup_hash"`
	NormalizedStem string `json:"normalized_stem"`
	CreatedAt      int64  `json:"created_at"`
}

type QuestionVersion struct {
	ID              string           `json:"id"`
	FormID          string           `json:"form_id"`
	SenseID         string           `json:"sense_id"`
	SenseVersionID  string           `json:"sense_version_id"`
	TaskFamily      string           `json:"task_family"`
	Stem            string           `json:"stem"`
	Options         []QuestionOption `json:"options"`
	CorrectOptionID string           `json:"correct_option_id"`
	Explanation     string           `json:"explanation"`
	CandidateID     *string          `json:"candidate_id,omitempty"`
	Status          string           `json:"status"`
	Revision        int64            `json:"revision"`
	CreatedAt       int64            `json:"created_at"`
	UpdatedAt       int64            `json:"updated_at"`
}

type QuestionInstance struct {
	ID                string `json:"id"`
	SessionID         string `json:"session_id"`
	Ordinal           int64  `json:"ordinal"`
	NormalPosition    *int64 `json:"normal_position,omitempty"`
	QuestionVersionID string `json:"question_version_id"`
	SnapshotJSON      string `json:"snapshot_json"`
	CreatedAt         int64  `json:"created_at"`
}

type PublicQuestion struct {
	InstanceID string         `json:"instance_id"`
	Ordinal    int64          `json:"ordinal"`
	TaskFamily string         `json:"task_family"`
	Stem       string         `json:"stem"`
	Options    []PublicOption `json:"options"`
}
