package content

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

// NormalizeText collapses whitespace and folds English case.
func NormalizeText(s string) string {
	fields := strings.Fields(strings.TrimSpace(s))
	return strings.ToLower(strings.Join(fields, " "))
}

// NormalizeLexeme normalizes a lexeme canonical form.
func NormalizeLexeme(s string) string {
	return NormalizeText(s)
}

// OptionCanonicalPayload is the structured payload hashed for option identity.
type OptionCanonicalPayload struct {
	Language       string `json:"language"`
	NormalizedText string `json:"normalized_text"`
	SenseID        string `json:"sense_id"`
	SourceID       string `json:"source_id"`
	SourceVersion  string `json:"source_version"`
}

// ComputeOptionCanonicalKey calculates the canonical SHA-256 key for an option.
func ComputeOptionCanonicalKey(language, text, senseID, sourceID, sourceVersion string) string {
	payload := OptionCanonicalPayload{
		Language:       strings.TrimSpace(language),
		NormalizedText: NormalizeText(text),
		SenseID:        strings.TrimSpace(senseID),
		SourceID:       strings.TrimSpace(sourceID),
		SourceVersion:  strings.TrimSpace(sourceVersion),
	}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ComputeFormDedupHash calculates the deduplication hash for a question form.
func ComputeFormDedupHash(senseID, taskFamily, stem string, optionCanonicalKeys []string) string {
	sortedKeys := make([]string, len(optionCanonicalKeys))
	copy(sortedKeys, optionCanonicalKeys)
	sort.Strings(sortedKeys)

	normalizedStem := NormalizeText(stem)
	raw := senseID + "\x1f" + taskFamily + "\x1f" + normalizedStem + "\x1f" + strings.Join(sortedKeys, "\x1f")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
