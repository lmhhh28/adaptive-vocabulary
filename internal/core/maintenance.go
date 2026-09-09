package core

import (
	"database/sql"
	"errors"
	"fmt"
)

var (
	ErrUnderMaintenance = errors.New("system is currently under maintenance")
)

type ProjectionControl struct {
	ID              int64
	Mode            string
	ActiveVersion   string
	RunID           sql.NullString
	RunStatus       sql.NullString
	RequiresRebuild bool
	FixedWatermark  int64
	UpdatedAt       int64
}

// GetProjectionControl loads the singleton row from projection_control.
func GetProjectionControl(querier interface {
	QueryRow(query string, args ...any) *sql.Row
}) (*ProjectionControl, error) {
	query := `
		SELECT id, mode, active_version, run_id, run_status, requires_rebuild, fixed_watermark, updated_at
		FROM projection_control
		WHERE id = 1
	`
	row := querier.QueryRow(query)
	var pc ProjectionControl
	var reqRebuild int
	err := row.Scan(
		&pc.ID,
		&pc.Mode,
		&pc.ActiveVersion,
		&pc.RunID,
		&pc.RunStatus,
		&reqRebuild,
		&pc.FixedWatermark,
		&pc.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan projection_control: %w", err)
	}
	pc.RequiresRebuild = (reqRebuild == 1)
	return &pc, nil
}

// CheckMaintenance returns ErrUnderMaintenance if projection_control.mode == "rebuilding".
func CheckMaintenance(querier interface {
	QueryRow(query string, args ...any) *sql.Row
}) error {
	pc, err := GetProjectionControl(querier)
	if err != nil {
		return err
	}
	if pc.Mode == "rebuilding" {
		return ErrUnderMaintenance
	}
	return nil
}
