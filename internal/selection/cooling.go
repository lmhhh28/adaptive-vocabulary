package selection

import (
	"context"
	"database/sql"
	"fmt"
)

// CheckNormalCooldown checks whether a lexeme is blocked by normal cooldown (5 distinct other lexemes or 24h new session).
func CheckNormalCooldown(ctx context.Context, db *sql.DB, cfg SelectionConfig, sessionID string, lexemeID string, nowMicro int64) (bool, error) {
	queryLatest := `
		SELECT e.observed_at, e.event_sequence, qi.session_id
		FROM exposure_event e
		JOIN question_instance qi ON qi.id = e.source_instance_id
		WHERE e.lexeme_id = ? AND e.kind = 'reserved'
		ORDER BY e.observed_at DESC, e.event_sequence DESC
		LIMIT 1
	`
	var lastObservedAt, lastSeq int64
	var resSessionID string
	err := db.QueryRowContext(ctx, queryLatest, lexemeID).Scan(&lastObservedAt, &lastSeq, &resSessionID)
	if err == sql.ErrNoRows {
		// Never reserved before -> no cooldown
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query latest reservation for lexeme: %w", err)
	}

	// Check 24-hour release in a new session first (fast exit without scanning history)
	if resSessionID != sessionID {
		diffMicro := nowMicro - lastObservedAt
		diffHours := float64(diffMicro) / (3600.0 * 1000000.0)
		if diffHours >= float64(cfg.NewSessionCooldownReleaseHours) {
			return false, nil
		}
	}

	// Count distinct other lexemes with normal or early_review practice submissions since that reservation.
	// Stop early once reaching cfg.NormalDistinctLexemeGap.
	queryDistinct := `
		SELECT count(*) FROM (
			SELECT DISTINCT s.lexeme_id
			FROM attempt_event a
			JOIN sense s ON s.id = a.sense_id
			WHERE s.lexeme_id != ?
			  AND a.source = 'practice'
			  AND a.purpose IN ('normal', 'early_review')
			  AND (a.observed_at > ? OR (a.observed_at = ? AND a.event_sequence > ?))
			LIMIT ?
		)
	`
	var distinctCount int
	err = db.QueryRowContext(ctx, queryDistinct, lexemeID, lastObservedAt, lastObservedAt, lastSeq, cfg.NormalDistinctLexemeGap).Scan(&distinctCount)
	if err != nil {
		return false, fmt.Errorf("count distinct other lexemes for normal cooldown: %w", err)
	}

	if distinctCount >= cfg.NormalDistinctLexemeGap {
		return false, nil
	}

	return true, nil
}

// CheckCorrectionEligibility checks if a pending correction item is cooling-eligible (3 distinct other lexemes).
func CheckCorrectionEligibility(ctx context.Context, db *sql.DB, cfg SelectionConfig, sessionID string, senseID string, taskFamily string, lexemeID string) (bool, int64, error) {
	queryCorr := `
		SELECT triggering_event_sequence, retries_count, status
		FROM correction_item
		WHERE session_id = ? AND sense_id = ? AND task_family = ?
	`
	var triggeringSeq int64
	var retries int
	var status string
	err := db.QueryRowContext(ctx, queryCorr, sessionID, senseID, taskFamily).Scan(&triggeringSeq, &retries, &status)
	if err == sql.ErrNoRows {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("query correction item: %w", err)
	}

	if status != "pending" || retries >= cfg.MaxCorrectionsPerUnitPerSession {
		return false, 0, nil
	}

	queryDistinct := `
		SELECT count(*) FROM (
			SELECT DISTINCT s.lexeme_id
			FROM attempt_event a
			JOIN sense s ON s.id = a.sense_id
			WHERE s.lexeme_id != ?
			  AND a.source = 'practice'
			  AND a.purpose IN ('normal', 'early_review', 'correction')
			  AND a.event_sequence > ?
			LIMIT ?
		)
	`
	var distinctCount int
	err = db.QueryRowContext(ctx, queryDistinct, lexemeID, triggeringSeq, cfg.CorrectionDistinctLexemeGap).Scan(&distinctCount)
	if err != nil {
		return false, 0, fmt.Errorf("count distinct other lexemes for correction: %w", err)
	}

	if distinctCount >= cfg.CorrectionDistinctLexemeGap {
		return true, triggeringSeq, nil
	}

	return false, triggeringSeq, nil
}
