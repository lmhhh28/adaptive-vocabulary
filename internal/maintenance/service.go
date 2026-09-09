package maintenance

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"adaptive-vocabulary/internal/core"
)

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

func (s *Service) DB() *sql.DB {
	return s.db
}

// GetMaintenanceState queries projection_control and calculates next_action.
func (s *Service) GetMaintenanceState(ctx context.Context) (*MaintenanceStatus, error) {
	pc, err := core.GetProjectionControl(s.db)
	if err != nil {
		return nil, fmt.Errorf("get projection control: %w", err)
	}

	var runIDPtr *string
	if pc.RunID.Valid && pc.RunID.String != "" {
		runIDPtr = &pc.RunID.String
	}

	var runStatusPtr *string
	if pc.RunStatus.Valid && pc.RunStatus.String != "" {
		runStatusPtr = &pc.RunStatus.String
	}

	var nextAction *string
	if pc.Mode == "rebuilding" {
		var na string
		if runStatusPtr != nil {
			switch *runStatusPtr {
			case "aborted_maintenance":
				na = "restart"
			case "failed", "running":
				na = "resume_or_abort"
			default:
				if pc.RequiresRebuild {
					na = "restart"
				} else {
					na = "resume_or_abort"
				}
			}
		} else {
			if pc.RequiresRebuild {
				na = "restart"
			} else {
				na = "resume_or_abort"
			}
		}
		nextAction = &na
	}

	return &MaintenanceStatus{
		Mode:            pc.Mode,
		RunID:           runIDPtr,
		RunStatus:       runStatusPtr,
		ActiveVersion:   pc.ActiveVersion,
		RequiresRebuild: pc.RequiresRebuild,
		NextAction:      nextAction,
	}, nil
}

// ComputeRevisionDigest computes a deterministic SHA-256 digest of all event_revision records.
func (s *Service) ComputeRevisionDigest(ctx context.Context, querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}) (string, error) {
	query := `
		SELECT id, event_id, event_type, revision_kind, reason, created_at
		FROM event_revision
		ORDER BY id ASC
	`
	rows, err := querier.Query(query)
	if err != nil {
		return "", fmt.Errorf("query event_revision for digest: %w", err)
	}
	defer rows.Close()

	h := sha256.New()
	for rows.Next() {
		var id, eventID, eventType, revKind, reason string
		var createdAt int64
		if err := rows.Scan(&id, &eventID, &eventType, &revKind, &reason, &createdAt); err != nil {
			return "", fmt.Errorf("scan event_revision: %w", err)
		}
		rowStr := fmt.Sprintf("%s|%s|%s|%s|%s|%d\n", id, eventID, eventType, revKind, reason, createdAt)
		h.Write([]byte(rowStr))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// NowMicro returns current UTC timestamp in microseconds.
func NowMicro() int64 {
	return time.Now().UTC().UnixNano() / 1000
}
