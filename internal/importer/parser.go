package importer

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"adaptive-vocabulary/internal/content"
)

const (
	MaxImportRows     = 5000
	MaxImportFileSize = 2 * 1024 * 1024 // 2 MiB
)

var (
	ErrFileTooLarge     = errors.New("import file exceeds 2 MiB limit")
	ErrTooManyRows      = errors.New("import input exceeds 5000 rows limit")
	ErrEmptyInput       = errors.New("import input is empty")
	ErrInvalidEncoding  = errors.New("input must be valid UTF-8")
	ErrInvalidCSVFormat = errors.New("invalid CSV format")
)

type ParsedEntry struct {
	Ordinal          int
	RawInput         string
	NormalizedWord   string
	SpecifiedSenseID *string
	IsInvalid        bool
	InvalidReason    string
}

// ParseRawInput extracts word entries from TXT or CSV or JSON text.
func ParseRawInput(raw []byte, filename string) ([]ParsedEntry, error) {
	if len(raw) == 0 {
		return nil, ErrEmptyInput
	}
	if len(raw) > MaxImportFileSize {
		return nil, ErrFileTooLarge
	}
	if !utf8.Valid(raw) {
		return nil, ErrInvalidEncoding
	}

	// Remove UTF-8 BOM if present
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))

	isCSV := strings.HasSuffix(strings.ToLower(filename), ".csv")
	if isCSV {
		return parseCSV(raw)
	}
	return parseTXT(raw)
}

func parseTXT(raw []byte) ([]ParsedEntry, error) {
	lines := strings.Split(string(raw), "\n")
	var entries []ParsedEntry
	ordinal := 0

	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue // skip blank lines
		}
		if len(entries) >= MaxImportRows {
			return nil, ErrTooManyRows
		}

		entry := validateAndBuildEntry(ordinal, trimmed, nil)
		entries = append(entries, entry)
		ordinal++
	}

	if len(entries) == 0 {
		return nil, ErrEmptyInput
	}
	return entries, nil
}

func parseCSV(raw []byte) ([]ParsedEntry, error) {
	r := csv.NewReader(bytes.NewReader(raw))
	r.FieldsPerRecord = -1 // flexible number of columns
	r.LazyQuotes = true

	var entries []ParsedEntry
	ordinal := 0
	isFirstRow := true

	for {
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidCSVFormat, err)
		}

		if len(record) == 0 {
			continue
		}

		// Check header
		col0 := strings.TrimSpace(record[0])
		if isFirstRow {
			isFirstRow = false
			lower0 := strings.ToLower(col0)
			if lower0 == "word" || lower0 == "lexeme" || lower0 == "vocabulary" {
				continue // skip header row
			}
		}

		if col0 == "" {
			continue
		}
		if len(entries) >= MaxImportRows {
			return nil, ErrTooManyRows
		}

		var specifiedSenseID *string
		if len(record) > 1 {
			sID := strings.TrimSpace(record[1])
			if sID != "" {
				specifiedSenseID = &sID
			}
		}

		rawLine := strings.Join(record, ",")
		entry := validateAndBuildEntry(ordinal, col0, specifiedSenseID)
		entry.RawInput = rawLine
		entries = append(entries, entry)
		ordinal++
	}

	if len(entries) == 0 {
		return nil, ErrEmptyInput
	}
	return entries, nil
}

// ParseWordSlice parses a slice of word strings.
func ParseWordSlice(words []string) ([]ParsedEntry, error) {
	if len(words) == 0 {
		return nil, ErrEmptyInput
	}
	if len(words) > MaxImportRows {
		return nil, ErrTooManyRows
	}

	var entries []ParsedEntry
	ordinal := 0
	for _, w := range words {
		trimmed := strings.TrimSpace(w)
		if trimmed == "" {
			continue
		}
		entries = append(entries, validateAndBuildEntry(ordinal, trimmed, nil))
		ordinal++
	}

	if len(entries) == 0 {
		return nil, ErrEmptyInput
	}
	return entries, nil
}

func validateAndBuildEntry(ordinal int, rawText string, specifiedSenseID *string) ParsedEntry {
	rawWord := strings.TrimSpace(rawText)
	norm := content.NormalizeLexeme(rawWord)

	entry := ParsedEntry{
		Ordinal:          ordinal,
		RawInput:         rawText,
		NormalizedWord:   norm,
		SpecifiedSenseID: specifiedSenseID,
	}

	if norm == "" {
		entry.IsInvalid = true
		entry.InvalidReason = "empty or whitespace-only word"
		return entry
	}

	if len(norm) > 100 {
		entry.IsInvalid = true
		entry.InvalidReason = "word exceeds 100 characters limit"
		return entry
	}

	// Basic check: should not be purely punctuation or control characters
	hasValidLetter := false
	for _, r := range norm {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '-' || r == '\'' {
			hasValidLetter = true
			break
		}
	}
	if !hasValidLetter {
		entry.IsInvalid = true
		entry.InvalidReason = "word contains no valid alphabetic characters"
		return entry
	}

	return entry
}
