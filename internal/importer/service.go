package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"adaptive-vocabulary/internal/content"
	"adaptive-vocabulary/internal/core"
)

var (
	ErrSourceNotFound      = errors.New("source not found")
	ErrBankNotFound        = errors.New("bank not found")
	ErrBankArchived        = errors.New("bank is archived")
	ErrImportNotFound      = errors.New("import job not found")
	ErrImportItemNotFound  = errors.New("import item not found")
	ErrRevisionConflict    = errors.New("revision conflict")
	ErrInvalidAction       = errors.New("invalid resolve action: must be 'adopt' or 'exclude'")
	ErrMissingSenseRefs    = errors.New("adopt action requires at least one sense_ref")
	ErrSenseNotFound       = errors.New("referenced sense does not exist")
	ErrSourceConflict      = errors.New("duplicate entry for source and version")
	ErrMaintenance         = errors.New("system under maintenance")
	ErrIdempotencyConflict = core.ErrIdempotencyConflict
	ErrInvalidItemStatus   = errors.New("import item cannot be resolved in its current status")
)

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

// ImportSourceEntries imports standard entries into a source.
func (s *Service) ImportSourceEntries(
	ctx context.Context,
	sourceID string,
	req SourceImportRequest,
	idempotencyKey string,
	digest string,
) (*ImportJob, error) {
	var job *ImportJob

	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		if err := core.CheckMaintenance(tc); err != nil {
			return ErrMaintenance
		}

		if idempotencyKey != "" {
			rec, err := core.CheckIdempotency(tc, "POST", "/api/v1/sources/"+sourceID+"/imports", idempotencyKey, digest)
			if err != nil {
				return err
			}
			if rec != nil {
				var cached ImportJob
				if err := json.Unmarshal([]byte(rec.ResponseBody), &cached); err == nil {
					job = &cached
					return nil
				}
			}
		}

		// Verify source exists and expected_revision matches
		var (
			currentRev    int64
			sourceVersion string
		)
		err := tc.QueryRow(`SELECT revision, source_version FROM source WHERE id = ?`, sourceID).Scan(&currentRev, &sourceVersion)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSourceNotFound
		}
		if err != nil {
			return fmt.Errorf("query source: %w", err)
		}
		if req.ExpectedRevision != currentRev {
			return ErrRevisionConflict
		}
		if req.SourceVersion != "" && req.SourceVersion != sourceVersion {
			return fmt.Errorf("source_version mismatch: expected %s, got %s", sourceVersion, req.SourceVersion)
		}

		now := time.Now().UTC().UnixMicro()
		importID := core.NewUUID()

		for _, e := range req.Entries {
			if strings.TrimSpace(e.ExternalEntryID) == "" || strings.TrimSpace(e.RawWord) == "" {
				continue
			}

			// Check duplicate
			var count int
			_ = tc.QueryRow(`
				SELECT COUNT(*) FROM source_entry
				WHERE source_id = ? AND source_version = ? AND external_entry_id = ?
			`, sourceID, sourceVersion, e.ExternalEntryID).Scan(&count)
			if count > 0 {
				return fmt.Errorf("%w: entry %s already exists", ErrSourceConflict, e.ExternalEntryID)
			}

			entryID := core.NewUUID()
			_, err = tc.Exec(`
				INSERT INTO source_entry (id, source_id, source_version, external_entry_id, raw_word, raw_translation, pos, tags, aliases, statistics, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, entryID, sourceID, sourceVersion, e.ExternalEntryID, e.RawWord, e.RawTranslation, e.POS, e.Tags, e.Aliases, e.Statistics, now)
			if err != nil {
				return fmt.Errorf("insert source_entry: %w", err)
			}

			// Register or reuse lexeme
			normWord := content.NormalizeLexeme(e.RawWord)
			if normWord == "" {
				continue
			}

			var lexemeID string
			err = tc.QueryRow(`SELECT id FROM lexeme WHERE canonical_form = ?`, normWord).Scan(&lexemeID)
			if errors.Is(err, sql.ErrNoRows) {
				lexemeID = core.NewUUID()
				_, err = tc.Exec(`INSERT INTO lexeme (id, canonical_form, created_at) VALUES (?, ?, ?)`, lexemeID, normWord, now)
				if err != nil {
					return fmt.Errorf("insert lexeme: %w", err)
				}
			} else if err != nil {
				return fmt.Errorf("query lexeme: %w", err)
			}

			// If aliases provided, register in lexeme_alias
			if e.Aliases != nil && strings.TrimSpace(*e.Aliases) != "" {
				aliasList := strings.Split(*e.Aliases, ",")
				for _, a := range aliasList {
					normAlias := content.NormalizeLexeme(a)
					if normAlias != "" && normAlias != normWord {
						_, _ = tc.Exec(`
							INSERT OR IGNORE INTO lexeme_alias (id, form, lexeme_id, created_at)
							VALUES (?, ?, ?, ?)
						`, core.NewUUID(), normAlias, lexemeID, now)
					}
				}
			}

			// If entry has a single clean meaning and part of speech, register standard sense
			rawTrans := strings.TrimSpace(e.RawTranslation)
			posStr := "n."
			if e.POS != nil && strings.TrimSpace(*e.POS) != "" {
				posStr = strings.TrimSpace(*e.POS)
			}
			if rawTrans != "" && !strings.Contains(rawTrans, "\n") && !strings.Contains(rawTrans, ";") {
				// Create sense if not already existing for this lexeme and source
				var existingSenseID string
				err = tc.QueryRow(`
					SELECT id FROM sense
					WHERE lexeme_id = ? AND part_of_speech = ? AND source_id = ?
				`, lexemeID, posStr, sourceID).Scan(&existingSenseID)
				if errors.Is(err, sql.ErrNoRows) {
					newSenseID := core.NewUUID()
					_, err = tc.Exec(`
						INSERT INTO sense (id, lexeme_id, part_of_speech, source_id, created_at)
						VALUES (?, ?, ?, ?, ?)
					`, newSenseID, lexemeID, posStr, sourceID, now)
					if err != nil {
						return fmt.Errorf("insert sense: %w", err)
					}

					_, err = tc.Exec(`
						INSERT INTO sense_version (id, sense_id, meaning, frequency, version_number, created_at)
						VALUES (?, ?, ?, NULL, 1, ?)
					`, core.NewUUID(), newSenseID, rawTrans, now)
					if err != nil {
						return fmt.Errorf("insert sense_version: %w", err)
					}
				}
			}
		}

		// Advance source revision
		_, err = tc.Exec(`UPDATE source SET revision = revision + 1, updated_at = ? WHERE id = ?`, now, sourceID)
		if err != nil {
			return fmt.Errorf("update source revision: %w", err)
		}

		// Record import job
		job = &ImportJob{
			ID:                      importID,
			TargetType:              "source",
			TargetID:                sourceID,
			Status:                  "completed",
			SourceVersion:           sourceVersion,
			ResolutionPolicyVersion: "v1",
			RawInputSummary:         fmt.Sprintf("Imported %d entries", len(req.Entries)),
			TotalCount:              len(req.Entries),
			ResolvedCount:           len(req.Entries),
			Revision:                0,
			CreatedAt:               now,
			UpdatedAt:               now,
		}

		_, err = tc.Exec(`
			INSERT INTO import_job (
				id, target_type, target_id, status, source_version, resolution_policy_version,
				raw_input_summary, total_count, resolved_count, ambiguous_count, not_found_count,
				invalid_count, excluded_count, revision, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 0, 0, ?, ?, ?)
		`, job.ID, job.TargetType, job.TargetID, job.Status, job.SourceVersion, job.ResolutionPolicyVersion,
			job.RawInputSummary, job.TotalCount, job.ResolvedCount, job.Revision, job.CreatedAt, job.UpdatedAt)
		if err != nil {
			return fmt.Errorf("insert import_job: %w", err)
		}

		if idempotencyKey != "" {
			respJSON, _ := json.Marshal(job)
			_ = core.SaveIdempotencyRecord(tc, &core.IdempotencyRecord{
				Operation:     "POST",
				ResourceScope: "/api/v1/sources/" + sourceID + "/imports",
				Key:           idempotencyKey,
				RequestDigest: digest,
				Status:        "committed",
				ResponseCode:  200,
				ResponseBody:  string(respJSON),
				CreatedAt:     now,
			})
		}

		return nil
	})

	if err != nil {
		return nil, err
	}
	return job, nil
}

// ImportBankVocabulary processes TXT/CSV entries and resolves them into bank senses.
func (s *Service) ImportBankVocabulary(
	ctx context.Context,
	bankID string,
	entries []ParsedEntry,
	resolutionPolicyVersion string,
	expectedRevision int64,
	idempotencyKey string,
	digest string,
) (*ImportJob, error) {
	if resolutionPolicyVersion == "" {
		resolutionPolicyVersion = "v1"
	}

	var job *ImportJob

	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		if err := core.CheckMaintenance(tc); err != nil {
			return ErrMaintenance
		}

		if idempotencyKey != "" {
			rec, err := core.CheckIdempotency(tc, "POST", "/api/v1/banks/"+bankID+"/imports", idempotencyKey, digest)
			if err != nil {
				return err
			}
			if rec != nil {
				var cached ImportJob
				if err := json.Unmarshal([]byte(rec.ResponseBody), &cached); err == nil {
					job = &cached
					return nil
				}
			}
		}

		// Verify bank exists and is not archived
		var (
			currentRev int64
			archived   int
		)
		err := tc.QueryRow(`SELECT revision, archived FROM bank WHERE id = ?`, bankID).Scan(&currentRev, &archived)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBankNotFound
		}
		if err != nil {
			return fmt.Errorf("query bank: %w", err)
		}
		if archived == 1 {
			return ErrBankArchived
		}
		if expectedRevision >= 0 && currentRev != expectedRevision {
			return ErrRevisionConflict
		}

		now := time.Now().UTC().UnixMicro()
		importID := core.NewUUID()

		// Insert parent import_job first so import_item foreign keys succeed
		_, err = tc.Exec(`
			INSERT INTO import_job (
				id, target_type, target_id, status, source_version, resolution_policy_version,
				raw_input_summary, total_count, resolved_count, ambiguous_count, not_found_count,
				invalid_count, excluded_count, revision, created_at, updated_at
			) VALUES (?, ?, ?, 'processing', NULL, ?, ?, 0, 0, 0, 0, 0, 0, 0, ?, ?)
		`, importID, "bank", bankID, resolutionPolicyVersion, fmt.Sprintf("Processing %d entries", len(entries)), now, now)
		if err != nil {
			return fmt.Errorf("insert parent import_job: %w", err)
		}

		var (
			totalCount     int
			resolvedCount  int
			ambiguousCount int
			notFoundCount  int
			invalidCount   int
			excludedCount  int
			bankSensesAdd  []string
		)

		for _, pe := range entries {
			totalCount++
			item := ImportItem{
				ID:                core.NewUUID(),
				ImportID:          importID,
				BankID:            &bankID,
				Ordinal:           pe.Ordinal,
				RawInput:          pe.RawInput,
				NormalizedWord:    pe.NormalizedWord,
				SpecifiedSenseID:  pe.SpecifiedSenseID,
				Revision:          0,
				CreatedAt:         now,
				UpdatedAt:         now,
				Candidates:        []SenseCandidate{},
				ResolvedSenseIDs:  []string{},
			}

			if pe.IsInvalid {
				item.Status = "invalid"
				item.DispositionReason = pe.InvalidReason
				invalidCount++
			} else if pe.SpecifiedSenseID != nil {
				// Specified sense_id from CSV
				sID := *pe.SpecifiedSenseID
				var exists int
				_ = tc.QueryRow(`SELECT COUNT(*) FROM sense WHERE id = ?`, sID).Scan(&exists)
				if exists > 0 {
					item.Status = "resolved"
					item.ResolvedSenseIDs = []string{sID}
					item.DispositionReason = "explicitly specified sense_id in input"
					resolvedCount++
					bankSensesAdd = append(bankSensesAdd, sID)
				} else {
					item.Status = "invalid"
					item.DispositionReason = fmt.Sprintf("specified sense_id %s not found in database", sID)
					invalidCount++
				}
			} else {
				// Automatic lexeme & sense matching
				var lexemeID string
				err := tc.QueryRow(`SELECT id FROM lexeme WHERE canonical_form = ?`, pe.NormalizedWord).Scan(&lexemeID)
				if errors.Is(err, sql.ErrNoRows) {
					// Check alias
					_ = tc.QueryRow(`SELECT lexeme_id FROM lexeme_alias WHERE form = ?`, pe.NormalizedWord).Scan(&lexemeID)
				}

				if lexemeID == "" {
					item.Status = "not_found"
					item.DispositionReason = "word not found in dictionary lexemes or aliases"
					notFoundCount++
				} else {
					// Load senses for this lexeme
					rows, err := tc.Query(`
						SELECT s.id, s.lexeme_id, s.part_of_speech, sv.meaning, s.source_id
						FROM sense s
						JOIN sense_version sv ON sv.id = (
							SELECT sv2.id FROM sense_version sv2
							WHERE sv2.sense_id = s.id
							ORDER BY sv2.version_number DESC, sv2.created_at DESC
							LIMIT 1
						)
						WHERE s.lexeme_id = ?
						ORDER BY s.created_at ASC
					`, lexemeID)
					if err != nil {
						return fmt.Errorf("query senses: %w", err)
					}

					var candidates []SenseCandidate
					for rows.Next() {
						var c SenseCandidate
						if err := rows.Scan(&c.SenseID, &c.LexemeID, &c.PartOfSpeech, &c.Meaning, &c.SourceID); err == nil {
							candidates = append(candidates, c)
						}
					}
					rows.Close()

					item.Candidates = candidates

					if len(candidates) == 0 {
						item.Status = "not_found"
						item.DispositionReason = "lexeme found but has no standard senses"
						notFoundCount++
					} else if len(candidates) == 1 {
						// Single unambiguous sense: automatically adopt
						item.Status = "resolved"
						item.ResolvedSenseIDs = []string{candidates[0].SenseID}
						item.DispositionReason = "unambiguous single sense match"
						resolvedCount++
						bankSensesAdd = append(bankSensesAdd, candidates[0].SenseID)
					} else {
						// Multiple candidate senses: ambiguous!
						item.Status = "ambiguous"
						item.DispositionReason = fmt.Sprintf("found %d candidate senses; requires human disambiguation", len(candidates))
						ambiguousCount++
					}
				}
			}

			// Insert import_item
			candJSON, _ := json.Marshal(item.Candidates)
			resolvedJSON, _ := json.Marshal(item.ResolvedSenseIDs)

			_, err = tc.Exec(`
				INSERT INTO import_item (
					id, import_id, bank_id, ordinal, raw_input, normalized_word, specified_sense_id,
					status, candidates_json, resolved_sense_ids_json, disposition_reason, revision,
					created_at, updated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, item.ID, item.ImportID, item.BankID, item.Ordinal, item.RawInput, item.NormalizedWord,
				item.SpecifiedSenseID, item.Status, string(candJSON), string(resolvedJSON),
				item.DispositionReason, item.Revision, item.CreatedAt, item.UpdatedAt)
			if err != nil {
				return fmt.Errorf("insert import_item: %w", err)
			}
		}

		// Add resolved senses to bank_sense
		newMembersAdded := 0
		for _, sID := range bankSensesAdd {
			res, err := tc.Exec(`
				INSERT OR IGNORE INTO bank_sense (bank_id, sense_id, added_at)
				VALUES (?, ?, ?)
			`, bankID, sID, now)
			if err != nil {
				return fmt.Errorf("insert bank_sense: %w", err)
			}
			aff, err := res.RowsAffected()
			if err != nil {
				return fmt.Errorf("bank_sense rows affected: %w", err)
			}
			if aff > 0 {
				newMembersAdded++
			}
		}

		// Advance bank revision if members changed
		if newMembersAdded > 0 {
			_, err = tc.Exec(`UPDATE bank SET revision = revision + 1, updated_at = ? WHERE id = ?`, now, bankID)
			if err != nil {
				return fmt.Errorf("advance bank revision: %w", err)
			}
		}

		job = &ImportJob{
			ID:                      importID,
			TargetType:              "bank",
			TargetID:                bankID,
			Status:                  "completed",
			ResolutionPolicyVersion: resolutionPolicyVersion,
			RawInputSummary:         fmt.Sprintf("Processed %d entries", totalCount),
			TotalCount:              totalCount,
			ResolvedCount:           resolvedCount,
			AmbiguousCount:          ambiguousCount,
			NotFoundCount:           notFoundCount,
			InvalidCount:            invalidCount,
			ExcludedCount:           excludedCount,
			Revision:                0,
			CreatedAt:               now,
			UpdatedAt:               now,
		}

		_, err = tc.Exec(`
			UPDATE import_job
			SET status = ?, raw_input_summary = ?, total_count = ?, resolved_count = ?,
			    ambiguous_count = ?, not_found_count = ?, invalid_count = ?, excluded_count = ?,
			    updated_at = ?
			WHERE id = ?
		`, job.Status, job.RawInputSummary, job.TotalCount, job.ResolvedCount,
			job.AmbiguousCount, job.NotFoundCount, job.InvalidCount, job.ExcludedCount,
			job.UpdatedAt, job.ID)
		if err != nil {
			return fmt.Errorf("update import_job: %w", err)
		}

		if idempotencyKey != "" {
			respJSON, _ := json.Marshal(job)
			_ = core.SaveIdempotencyRecord(tc, &core.IdempotencyRecord{
				Operation:     "POST",
				ResourceScope: "/api/v1/banks/" + bankID + "/imports",
				Key:           idempotencyKey,
				RequestDigest: digest,
				Status:        "committed",
				ResponseCode:  200,
				ResponseBody:  string(respJSON),
				CreatedAt:     now,
			})
		}

		return nil
	})

	if err != nil {
		return nil, err
	}
	return job, nil
}

// GetImportItems fetches items for an import job with optional status filter.
func (s *Service) GetImportItems(
	ctx context.Context,
	importID string,
	statusFilter string,
	limit int,
	offset int,
) (*ImportItemsResponse, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}

	// Verify job exists and fetch counts
	var jobExists int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM import_job WHERE id = ?`, importID).Scan(&jobExists)
	if err != nil || jobExists == 0 {
		return nil, ErrImportNotFound
	}

	counts := map[string]int{
		"resolved":  0,
		"ambiguous": 0,
		"not_found": 0,
		"invalid":   0,
		"excluded":  0,
	}

	rowsCounts, err := s.db.QueryContext(ctx, `
		SELECT status, COUNT(*)
		FROM import_item
		WHERE import_id = ?
		GROUP BY status
	`, importID)
	if err == nil {
		for rowsCounts.Next() {
			var st string
			var c int
			if err := rowsCounts.Scan(&st, &c); err == nil {
				counts[st] = c
			}
		}
		rowsCounts.Close()
	}

	total := 0
	for _, v := range counts {
		total += v
	}

	// Build query
	var query string
	var args []any
	if statusFilter != "" {
		query = `
			SELECT id, import_id, bank_id, ordinal, raw_input, normalized_word, specified_sense_id,
			       status, candidates_json, resolved_sense_ids_json, disposition_reason, revision,
			       created_at, updated_at
			FROM import_item
			WHERE import_id = ? AND status = ?
			ORDER BY ordinal ASC
			LIMIT ? OFFSET ?
		`
		args = []any{importID, statusFilter, limit, offset}
	} else {
		query = `
			SELECT id, import_id, bank_id, ordinal, raw_input, normalized_word, specified_sense_id,
			       status, candidates_json, resolved_sense_ids_json, disposition_reason, revision,
			       created_at, updated_at
			FROM import_item
			WHERE import_id = ?
			ORDER BY ordinal ASC
			LIMIT ? OFFSET ?
		`
		args = []any{importID, limit, offset}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query import_items: %w", err)
	}
	defer rows.Close()

	items := make([]ImportItem, 0)
	for rows.Next() {
		var item ImportItem
		var (
			bankID           sql.NullString
			specifiedSenseID sql.NullString
			candJSON         string
			resolvedJSON     string
			dispReason       sql.NullString
		)

		err := rows.Scan(
			&item.ID, &item.ImportID, &bankID, &item.Ordinal, &item.RawInput, &item.NormalizedWord,
			&specifiedSenseID, &item.Status, &candJSON, &resolvedJSON, &dispReason,
			&item.Revision, &item.CreatedAt, &item.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan import_item: %w", err)
		}

		if bankID.Valid {
			item.BankID = &bankID.String
		}
		if specifiedSenseID.Valid {
			item.SpecifiedSenseID = &specifiedSenseID.String
		}
		if dispReason.Valid {
			item.DispositionReason = dispReason.String
		}

		item.Candidates = make([]SenseCandidate, 0)
		_ = json.Unmarshal([]byte(candJSON), &item.Candidates)

		item.ResolvedSenseIDs = make([]string, 0)
		_ = json.Unmarshal([]byte(resolvedJSON), &item.ResolvedSenseIDs)

		items = append(items, item)
	}

	return &ImportItemsResponse{
		Items:  items,
		Total:  total,
		Counts: counts,
	}, nil
}

// ResolveImportItem handles manual disambiguation (adopt or exclude).
func (s *Service) ResolveImportItem(
	ctx context.Context,
	itemID string,
	req ResolveItemRequest,
	idempotencyKey string,
	digest string,
) (*ImportItem, error) {
	if req.Action != "adopt" && req.Action != "exclude" {
		return nil, ErrInvalidAction
	}
	if req.Action == "adopt" && len(req.SenseRefs) == 0 {
		return nil, ErrMissingSenseRefs
	}

	var updatedItem *ImportItem

	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		if err := core.CheckMaintenance(tc); err != nil {
			return ErrMaintenance
		}

		if idempotencyKey != "" {
			rec, err := core.CheckIdempotency(tc, "POST", "/api/v1/import-items/"+itemID+"/resolve", idempotencyKey, digest)
			if err != nil {
				return err
			}
			if rec != nil {
				var cached ImportItem
				if err := json.Unmarshal([]byte(rec.ResponseBody), &cached); err == nil {
					updatedItem = &cached
					return nil
				}
			}
		}

		// 1. Fetch current item
		var (
			importID         string
			bankID           sql.NullString
			ordinal          int
			rawInput         string
			normWord         string
			specifiedSenseID sql.NullString
			status           string
			candJSON         string
			resolvedJSON     string
			dispReason       sql.NullString
			currentRev       int64
			createdAt        int64
		)

		err := tc.QueryRow(`
			SELECT import_id, bank_id, ordinal, raw_input, normalized_word, specified_sense_id,
			       status, candidates_json, resolved_sense_ids_json, disposition_reason, revision, created_at
			FROM import_item
			WHERE id = ?
		`, itemID).Scan(
			&importID, &bankID, &ordinal, &rawInput, &normWord, &specifiedSenseID,
			&status, &candJSON, &resolvedJSON, &dispReason, &currentRev, &createdAt,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrImportItemNotFound
		}
		if err != nil {
			return fmt.Errorf("query import_item: %w", err)
		}

		// 2. CAS check
		if req.ExpectedRevision >= 0 && currentRev != req.ExpectedRevision {
			return ErrRevisionConflict
		}

		// Status check: only ambiguous or not_found items can be resolved/excluded
		if status != "ambiguous" && status != "not_found" {
			return ErrInvalidItemStatus
		}

		now := time.Now().UTC().UnixMicro()
		newRev := currentRev + 1
		oldStatus := status

		var newResolvedIDs []string
		_ = json.Unmarshal([]byte(resolvedJSON), &newResolvedIDs)

		if req.Action == "adopt" {
			// Validate that all senseRefs exist
			for _, sID := range req.SenseRefs {
				var exists int
				_ = tc.QueryRow(`SELECT COUNT(*) FROM sense WHERE id = ?`, sID).Scan(&exists)
				if exists == 0 {
					return fmt.Errorf("%w: %s", ErrSenseNotFound, sID)
				}
			}

			// Add to bank_sense
			if bankID.Valid && bankID.String != "" {
				membersAdded := 0
				for _, sID := range req.SenseRefs {
					res, err := tc.Exec(`
						INSERT OR IGNORE INTO bank_sense (bank_id, sense_id, added_at)
						VALUES (?, ?, ?)
					`, bankID.String, sID, now)
					if err != nil {
						return fmt.Errorf("insert bank_sense: %w", err)
					}
					aff, err := res.RowsAffected()
					if err != nil {
						return fmt.Errorf("bank_sense rows affected: %w", err)
					}
					if aff > 0 {
						membersAdded++
					}
				}
				if membersAdded > 0 {
					_, err = tc.Exec(`UPDATE bank SET revision = revision + 1, updated_at = ? WHERE id = ?`, now, bankID.String)
					if err != nil {
						return fmt.Errorf("advance bank revision: %w", err)
					}
				}
			}

			status = "resolved"
			newResolvedIDs = req.SenseRefs
		} else if req.Action == "exclude" {
			status = "excluded"
		}

		resolvedBlob, _ := json.Marshal(newResolvedIDs)
		_, err = tc.Exec(`
			UPDATE import_item
			SET status = ?, resolved_sense_ids_json = ?, disposition_reason = ?, revision = ?, updated_at = ?
			WHERE id = ?
		`, status, string(resolvedBlob), req.Reason, newRev, now, itemID)
		if err != nil {
			return fmt.Errorf("update import_item: %w", err)
		}

		// Update import_job counts if status changed
		if oldStatus != status {
			updateColOld := countColumnName(oldStatus)
			updateColNew := countColumnName(status)
			if updateColOld != "" && updateColNew != "" {
				_, _ = tc.Exec(fmt.Sprintf(`
					UPDATE import_job
					SET %s = MAX(0, %s - 1), %s = %s + 1, updated_at = ?
					WHERE id = ?
				`, updateColOld, updateColOld, updateColNew, updateColNew), now, importID)
			}
		}

		var candidates []SenseCandidate
		_ = json.Unmarshal([]byte(candJSON), &candidates)

		updatedItem = &ImportItem{
			ID:                itemID,
			ImportID:          importID,
			Ordinal:           ordinal,
			RawInput:          rawInput,
			NormalizedWord:    normWord,
			Status:            status,
			Candidates:        candidates,
			ResolvedSenseIDs:  newResolvedIDs,
			DispositionReason: req.Reason,
			Revision:          newRev,
			CreatedAt:         createdAt,
			UpdatedAt:         now,
		}
		if bankID.Valid {
			updatedItem.BankID = &bankID.String
		}
		if specifiedSenseID.Valid {
			updatedItem.SpecifiedSenseID = &specifiedSenseID.String
		}

		if idempotencyKey != "" {
			respJSON, _ := json.Marshal(updatedItem)
			_ = core.SaveIdempotencyRecord(tc, &core.IdempotencyRecord{
				Operation:     "POST",
				ResourceScope: "/api/v1/import-items/" + itemID + "/resolve",
				Key:           idempotencyKey,
				RequestDigest: digest,
				Status:        "committed",
				ResponseCode:  200,
				ResponseBody:  string(respJSON),
				CreatedAt:     now,
			})
		}

		return nil
	})

	if err != nil {
		return nil, err
	}
	return updatedItem, nil
}

func countColumnName(status string) string {
	switch status {
	case "resolved":
		return "resolved_count"
	case "ambiguous":
		return "ambiguous_count"
	case "not_found":
		return "not_found_count"
	case "invalid":
		return "invalid_count"
	case "excluded":
		return "excluded_count"
	default:
		return ""
	}
}
