package learning

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"adaptive-vocabulary/internal/core"
)

const Exposure24HoursMicro = int64(24 * 3600 * 1000000)

// RecordExposure logs an exposure event in an atomic transaction
func RecordExposure(tc *core.TxnContext, exp *ExposureEvent) error {
	if exp.ID == "" {
		exp.ID = core.NewUUID()
	}
	if exp.CreatedAt == 0 {
		exp.CreatedAt = time.Now().UTC().UnixNano() / 1000
	}
	if exp.ObservedAt == 0 {
		exp.ObservedAt = exp.CreatedAt
	}

	// Allocate event sequence if not already assigned
	if exp.EventSequence == 0 {
		res, err := tc.Exec(
			"INSERT INTO global_sequence (event_name, created_at) VALUES (?, ?)",
			"exposure_"+exp.Kind,
			exp.ObservedAt,
		)
		if err != nil {
			return fmt.Errorf("allocate global sequence for exposure: %w", err)
		}
		seq, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("get exposure sequence id: %w", err)
		}
		exp.EventSequence = seq
	}

	query := `
		INSERT INTO exposure_event (
			id, source_instance_id, kind, sense_id, lexeme_id, form_id,
			observed_at, event_sequence, client_displayed_at, display_event_id, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (source_instance_id, kind) WHERE kind IN ('reserved', 'feedback_available')
		DO NOTHING
	`
	_, err := tc.Exec(
		query,
		exp.ID,
		exp.SourceInstanceID,
		exp.Kind,
		exp.SenseID,
		exp.LexemeID,
		exp.FormID,
		exp.ObservedAt,
		exp.EventSequence,
		exp.ClientDisplayedAt,
		exp.DisplayEventID,
		exp.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert exposure event: %w", err)
	}

	return nil
}

// DeriveExposureLabels checks exposure history for the given form & lexeme
// strictly excluding currentInstanceID and strictly before cutoff (R04).
func DeriveExposureLabels(
	ctx context.Context,
	tx *sql.Tx,
	currentInstanceID string,
	formID string,
	lexemeID string,
	cutoffObservedAt int64,
	cutoffSequence int64,
) (hasSameForm bool, hasRecentRelated bool, err error) {
	query := `
		SELECT form_id, lexeme_id, observed_at
		FROM exposure_event
		WHERE source_instance_id != ?
		  AND (observed_at < ? OR (observed_at = ? AND event_sequence < ?))
	`
	rows, err := tx.QueryContext(ctx, query, currentInstanceID, cutoffObservedAt, cutoffObservedAt, cutoffSequence)
	if err != nil {
		return false, false, fmt.Errorf("query exposure events: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var rowFormID, rowLexemeID string
		var rowObservedAt int64
		if err := rows.Scan(&rowFormID, &rowLexemeID, &rowObservedAt); err != nil {
			return false, false, fmt.Errorf("scan exposure row: %w", err)
		}

		if rowFormID == formID {
			hasSameForm = true
		}

		if rowLexemeID == lexemeID {
			diff := cutoffObservedAt - rowObservedAt
			if diff >= 0 && diff <= Exposure24HoursMicro {
				hasRecentRelated = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		return false, false, fmt.Errorf("iterate exposure rows: %w", err)
	}

	return hasSameForm, hasRecentRelated, nil
}
