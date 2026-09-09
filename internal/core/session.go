package core

import (
	"database/sql"
	"errors"
	"fmt"
)

var (
	ErrActiveSessionExists = errors.New("an active session already exists")
)

type Session struct {
	ID              string         `json:"id"`
	Kind            string         `json:"kind"`
	Status          string         `json:"status"`
	BankID          sql.NullString `json:"bank_id,omitempty"`
	Cursor          int64          `json:"cursor"`
	NormalPosition  sql.NullInt64  `json:"normal_position,omitempty"`
	Revision        int64          `json:"revision"`
	LastAckSequence int64          `json:"last_ack_sequence"`
	DeadlineAt      sql.NullInt64  `json:"deadline_at,omitempty"`
	CreatedAt       int64          `json:"created_at"`
	UpdatedAt       int64          `json:"updated_at"`
}

// GetActiveSession finds the currently active (status='open') session, if any.
func GetActiveSession(querier interface {
	QueryRow(query string, args ...any) *sql.Row
}) (*Session, error) {
	query := `
		SELECT id, kind, status, bank_id, cursor, normal_position, revision, last_ack_sequence, deadline_at, created_at, updated_at
		FROM session
		WHERE status = 'open'
	`
	row := querier.QueryRow(query)
	var s Session
	err := row.Scan(
		&s.ID,
		&s.Kind,
		&s.Status,
		&s.BankID,
		&s.Cursor,
		&s.NormalPosition,
		&s.Revision,
		&s.LastAckSequence,
		&s.DeadlineAt,
		&s.CreatedAt,
		&s.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get active session: %w", err)
	}
	return &s, nil
}
