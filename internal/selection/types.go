package selection

import (
	"encoding/json"
	"fmt"
	"os"

	"adaptive-vocabulary/internal/content"
)

type SelectionConfig struct {
	UnseenEveryNormalPositions     int      `json:"unseen_every_normal_positions"`
	FirstUnseenPosition            int      `json:"first_unseen_position"`
	NormalDistinctLexemeGap        int      `json:"normal_distinct_lexeme_gap"`
	CorrectionDistinctLexemeGap    int      `json:"correction_distinct_lexeme_gap"`
	MaxCorrectionsPerUnitPerSession int     `json:"max_corrections_per_unit_per_session"`
	NewSessionCooldownReleaseHours int      `json:"new_session_cooldown_release_hours"`
	RecentExposureHours            int      `json:"recent_exposure_hours"`
	ConfusionMinWrongSelections    int      `json:"confusion_min_wrong_selections"`
	NormalPositionPurposes         []string `json:"normal_position_purposes"`
}

func DefaultSelectionConfig() SelectionConfig {
	return SelectionConfig{
		UnseenEveryNormalPositions:     4,
		FirstUnseenPosition:            1,
		NormalDistinctLexemeGap:        5,
		CorrectionDistinctLexemeGap:    3,
		MaxCorrectionsPerUnitPerSession: 2,
		NewSessionCooldownReleaseHours: 24,
		RecentExposureHours:            24,
		ConfusionMinWrongSelections:    2,
		NormalPositionPurposes:         []string{"normal", "early_review"},
	}
}

func LoadSelectionConfigFromPath(path string) (*SelectionConfig, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read policy file %q: %w", path, err)
	}

	var root struct {
		Selection SelectionConfig `json:"selection"`
	}
	if err := json.Unmarshal(bytes, &root); err != nil {
		return nil, fmt.Errorf("unmarshal selection policy JSON: %w", err)
	}

	return &root.Selection, nil
}

type SelectionRequest struct {
	SessionID         string `json:"session_id"`
	BankID            string `json:"bank_id"`
	NormalPosition    int    `json:"normal_position"` // 1-based index
	ProjectionVersion string `json:"projection_version"`
	NowMicro          int64  `json:"now_micro"`
}

type SelectionResult struct {
	Status              string                   `json:"status"` // "ready" or "unavailable"
	Reason              string                   `json:"reason,omitempty"`
	CandidateGroup      string                   `json:"candidate_group,omitempty"` // "due", "unseen", "early_review", "correction"
	IsCorrection        bool                     `json:"is_correction"`
	QuestionVersion     *content.QuestionVersion `json:"question_version,omitempty"`
	FormID              string                   `json:"form_id,omitempty"`
	SenseID             string                   `json:"sense_id,omitempty"`
	LexemeID            string                   `json:"lexeme_id,omitempty"`
	TaskFamily          string                   `json:"task_family,omitempty"`
	CooldownCount       int                      `json:"cooldown_count"`
	ContentMissingCount int                      `json:"content_missing_count"`
	ExhaustedCount      int                      `json:"exhausted_count"`
	SelectionReason     string                   `json:"selection_reason,omitempty"`
}
