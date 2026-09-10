package maintenance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"adaptive-vocabulary/internal/core"
	"adaptive-vocabulary/internal/learning"
)

var (
	ErrRebuildConflict     = errors.New("rebuild state conflict")
	ErrPredecessorConflict = errors.New("predecessor target projection conflict")
)

// StartRebuildRun initiates a rebuild run in transaction, setting projection_control.mode = 'rebuilding'.
func (s *Service) StartRebuildRun(ctx context.Context, kind, targetPolicy, targetProjectionVersion string) (*ModelRebuildRun, error) {
	if kind == "" {
		kind = "evidence_revision"
	}
	if targetPolicy == "" {
		targetPolicy = "policy-v2"
	}

	var run *ModelRebuildRun
	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		pc, err := core.GetProjectionControl(tc)
		if err != nil {
			return fmt.Errorf("get projection control: %w", err)
		}

		runID := core.NewUUID()
		if targetProjectionVersion == "" {
			targetProjectionVersion = "v_" + runID[:8]
		}

		digest, err := s.ComputeRevisionDigest(ctx, tc)
		if err != nil {
			return fmt.Errorf("compute revision digest: %w", err)
		}

		var watermark int64
		err = tc.QueryRow("SELECT coalesce(max(seq), 0) FROM global_sequence").Scan(&watermark)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("get max global sequence watermark: %w", err)
		}

		now := NowMicro()
		run = &ModelRebuildRun{
			ID:                      runID,
			Kind:                    kind,
			BaseActiveVersion:       pc.ActiveVersion,
			TargetProjectionVersion: targetProjectionVersion,
			TargetPolicy:            targetPolicy,
			BaseRevisionDigest:      digest,
			FixedWatermark:          watermark,
			Status:                  "running",
			RequiresRebuild:         true,
			CreatedAt:               now,
			UpdatedAt:               now,
		}

		insertQuery := `
			INSERT INTO model_rebuild_run (
				id, kind, base_active_version, target_projection_version, target_policy,
				base_revision_digest, fixed_watermark, status, requires_rebuild,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, 'running', 1, ?, ?)
		`
		_, err = tc.Exec(insertQuery,
			run.ID, run.Kind, run.BaseActiveVersion, run.TargetProjectionVersion, run.TargetPolicy,
			run.BaseRevisionDigest, run.FixedWatermark, now, now,
		)
		if err != nil {
			return fmt.Errorf("insert model_rebuild_run: %w", err)
		}

		updatePC := `
			UPDATE projection_control
			SET mode = 'rebuilding', run_id = ?, run_status = 'running',
			    requires_rebuild = 1, fixed_watermark = ?, updated_at = ?
			WHERE id = 1
		`
		_, err = tc.Exec(updatePC, run.ID, watermark, now)
		if err != nil {
			return fmt.Errorf("update projection_control to rebuilding: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return run, nil
}

func loadFSRSPolicy() learning.FSRSPolicyConfig {
	candidates := []string{
		"config/policy-v2.json",
		"../config/policy-v2.json",
		"../../config/policy-v2.json",
	}
	for _, c := range candidates {
		if p, err := learning.LoadFSRSPolicyFromPath(c); err == nil && p != nil {
			return *p
		}
	}
	return learning.FSRSPolicyConfig{}
}

// ExecuteReplay replays projections up to run.FixedWatermark and atomically switches active_version on success.
func (s *Service) ExecuteReplay(ctx context.Context, run *ModelRebuildRun) error {
	var policy learning.FSRSPolicyConfig
	if s.fsrsPolicy != nil && len(s.fsrsPolicy.Weights) > 0 {
		policy = *s.fsrsPolicy
	} else {
		policy = loadFSRSPolicy()
	}
	if len(policy.Weights) == 0 {
		return errors.New("cannot execute replay: FSRS policy configuration is missing or invalid")
	}

	learnSvc := learning.NewLearningService(s.db, policy, "observation-v2", run.TargetProjectionVersion)
	if err := learnSvc.ReplayProjections(ctx, run.TargetProjectionVersion, run.FixedWatermark); err != nil {
		now := NowMicro()
		errMsg := err.Error()
		_ = core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
			_, _ = tc.Exec("UPDATE model_rebuild_run SET status = 'failed', error_message = ?, updated_at = ? WHERE id = ?", errMsg, now, run.ID)
			_, _ = tc.Exec("UPDATE projection_control SET run_status = 'failed', requires_rebuild = 1, updated_at = ? WHERE id = 1", now)
			return nil
		})
		run.Status = "failed"
		run.ErrorMessage = &errMsg
		return fmt.Errorf("replay projections: %w", err)
	}

	// Replay succeeded: switch active_version atomically
	now := NowMicro()
	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		res, err := tc.Exec(
			"UPDATE model_rebuild_run SET status = 'succeeded', updated_at = ? WHERE id = ? AND status IN ('running', 'failed')",
			now, run.ID,
		)
		if err != nil {
			return fmt.Errorf("update run status to succeeded: %w", err)
		}
		rows, _ := res.RowsAffected()
		if rows == 0 {
			return ErrRebuildConflict
		}

		res2, err := tc.Exec(
			"UPDATE projection_control SET mode = 'active', active_version = ?, run_status = 'succeeded', requires_rebuild = 0, updated_at = ? WHERE id = 1",
			run.TargetProjectionVersion, now,
		)
		if err != nil {
			return fmt.Errorf("switch active projection: %w", err)
		}
		rows2, _ := res2.RowsAffected()
		if rows2 == 0 {
			return ErrRebuildConflict
		}
		return nil
	})
	if err != nil {
		return err
	}

	run.Status = "succeeded"
	run.RequiresRebuild = false
	return nil
}

// GetRun loads a model_rebuild_run by ID.
func (s *Service) GetRun(ctx context.Context, runID string) (*ModelRebuildRun, error) {
	query := `
		SELECT id, kind, base_active_version, target_projection_version, target_policy,
		       base_revision_digest, fixed_watermark, status, requires_rebuild,
		       predecessor_run_id, error_message, created_at, updated_at
		FROM model_rebuild_run
		WHERE id = ?
	`
	row := s.db.QueryRowContext(ctx, query, runID)
	var r ModelRebuildRun
	var predID, errMsg sql.NullString
	var reqReb int
	err := row.Scan(
		&r.ID, &r.Kind, &r.BaseActiveVersion, &r.TargetProjectionVersion, &r.TargetPolicy,
		&r.BaseRevisionDigest, &r.FixedWatermark, &r.Status, &reqReb,
		&predID, &errMsg, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("rebuild run not found: %s", runID)
		}
		return nil, fmt.Errorf("query rebuild run: %w", err)
	}
	r.RequiresRebuild = (reqReb == 1)
	if predID.Valid {
		r.PredecessorRunID = &predID.String
	}
	if errMsg.Valid {
		r.ErrorMessage = &errMsg.String
	}
	return &r, nil
}

// Resume re-executes replay for a failed or running run.
func (s *Service) Resume(ctx context.Context, runID string) (*RebuildResult, error) {
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}

	if run.Status == "succeeded" {
		return nil, fmt.Errorf("%w: cannot resume already succeeded run", ErrRebuildConflict)
	}
	if run.Status == "aborted" || run.Status == "aborted_maintenance" {
		return nil, fmt.Errorf("%w: cannot resume aborted run", ErrRebuildConflict)
	}

	// Attempt replay
	replayErr := s.ExecuteReplay(ctx, run)
	pc, err := core.GetProjectionControl(s.db)
	if err != nil {
		return nil, err
	}

	nextAction := "none"
	if run.Status != "succeeded" {
		nextAction = "resume_or_abort"
	}

	res := &RebuildResult{
		RunID:           run.ID,
		Status:          run.Status,
		Mode:            pc.Mode,
		ActiveVersion:   pc.ActiveVersion,
		RequiresRebuild: pc.RequiresRebuild,
		NextAction:      nextAction,
	}

	if replayErr != nil {
		return res, replayErr
	}
	return res, nil
}

// Abort handles aborting a rebuild run according to R03 table.
func (s *Service) Abort(ctx context.Context, runID string) (*RebuildResult, error) {
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}

	if run.Status == "succeeded" {
		return nil, fmt.Errorf("%w: cannot abort succeeded run", ErrRebuildConflict)
	}

	// If already aborted, return idempotent result
	if run.Status == "aborted" || run.Status == "aborted_maintenance" {
		pc, _ := core.GetProjectionControl(s.db)
		nextAction := "none"
		if run.Status == "aborted_maintenance" {
			nextAction = "restart"
		}
		return &RebuildResult{
			RunID:           run.ID,
			Status:          run.Status,
			Mode:            pc.Mode,
			ActiveVersion:   pc.ActiveVersion,
			RequiresRebuild: pc.RequiresRebuild,
			NextAction:      nextAction,
		}, nil
	}

	var result *RebuildResult
	err = core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		pc, err := core.GetProjectionControl(tc)
		if err != nil {
			return err
		}

		currentDigest, err := s.ComputeRevisionDigest(ctx, tc)
		if err != nil {
			return fmt.Errorf("compute current revision digest: %w", err)
		}

		now := NowMicro()
		if run.Kind == "policy_only" && currentDigest == run.BaseRevisionDigest {
			// policy_only abort safely unlocks gate and restores base_active_version
			res1, err := tc.Exec(
				"UPDATE model_rebuild_run SET status = 'aborted', updated_at = ? WHERE id = ? AND status IN ('running', 'failed')",
				now, run.ID,
			)
			if err != nil {
				return err
			}
			rows, _ := res1.RowsAffected()
			if rows == 0 {
				return ErrRebuildConflict
			}

			res2, err := tc.Exec(
				"UPDATE projection_control SET mode = 'active', active_version = ?, run_status = 'aborted', requires_rebuild = 0, updated_at = ? WHERE id = 1",
				run.BaseActiveVersion, now,
			)
			if err != nil {
				return err
			}
			rows2, _ := res2.RowsAffected()
			if rows2 == 0 {
				return ErrRebuildConflict
			}

			result = &RebuildResult{
				RunID:           run.ID,
				Status:          "aborted",
				Mode:            "active",
				ActiveVersion:   run.BaseActiveVersion,
				RequiresRebuild: false,
				NextAction:      "none",
			}
		} else {
			// evidence_revision or digest changed: stays in maintenance!
			res1, err := tc.Exec(
				"UPDATE model_rebuild_run SET status = 'aborted_maintenance', updated_at = ? WHERE id = ? AND status IN ('running', 'failed')",
				now, run.ID,
			)
			if err != nil {
				return err
			}
			rows, _ := res1.RowsAffected()
			if rows == 0 {
				return ErrRebuildConflict
			}

			res2, err := tc.Exec(
				"UPDATE projection_control SET mode = 'rebuilding', run_status = 'aborted_maintenance', requires_rebuild = 1, updated_at = ? WHERE id = 1",
				now,
			)
			if err != nil {
				return err
			}
			rows2, _ := res2.RowsAffected()
			if rows2 == 0 {
				return ErrRebuildConflict
			}

			result = &RebuildResult{
				RunID:           run.ID,
				Status:          "aborted_maintenance",
				Mode:            "rebuilding",
				ActiveVersion:   pc.ActiveVersion,
				RequiresRebuild: true,
				NextAction:      "restart",
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// Restart creates a new rebuild run linking predecessor_run_id and executes replay.
func (s *Service) Restart(ctx context.Context, predecessorRunID, newTargetProjectionVersion string) (*RebuildResult, error) {
	pred, err := s.GetRun(ctx, predecessorRunID)
	if err != nil {
		return nil, err
	}

	if pred.Status != "aborted_maintenance" && pred.Status != "failed" {
		return nil, fmt.Errorf("%w: predecessor run must be aborted_maintenance or failed, got %s", ErrRebuildConflict, pred.Status)
	}

	// Check if a successor run already exists
	querySucc := `
		SELECT id, kind, base_active_version, target_projection_version, target_policy,
		       base_revision_digest, fixed_watermark, status, requires_rebuild,
		       predecessor_run_id, error_message, created_at, updated_at
		FROM model_rebuild_run
		WHERE predecessor_run_id = ?
	`
	row := s.db.QueryRowContext(ctx, querySucc, predecessorRunID)
	var succ ModelRebuildRun
	var pID, eMsg sql.NullString
	var reqReb int
	err = row.Scan(
		&succ.ID, &succ.Kind, &succ.BaseActiveVersion, &succ.TargetProjectionVersion, &succ.TargetPolicy,
		&succ.BaseRevisionDigest, &succ.FixedWatermark, &succ.Status, &reqReb,
		&pID, &eMsg, &succ.CreatedAt, &succ.UpdatedAt,
	)
	if err == nil {
		// Successor already exists: check target version
		if succ.TargetProjectionVersion != newTargetProjectionVersion {
			return nil, fmt.Errorf("%w: existing successor has target %s != %s", ErrPredecessorConflict, succ.TargetProjectionVersion, newTargetProjectionVersion)
		}
		pc, _ := core.GetProjectionControl(s.db)
		nextAction := "none"
		if succ.Status != "succeeded" {
			nextAction = "resume_or_abort"
		}
		return &RebuildResult{
			RunID:           succ.ID,
			Status:          succ.Status,
			Mode:            pc.Mode,
			ActiveVersion:   pc.ActiveVersion,
			RequiresRebuild: pc.RequiresRebuild,
			NextAction:      nextAction,
		}, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("check successor run: %w", err)
	}

	// Create new successor run
	var newRun *ModelRebuildRun
	err = core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		digest, err := s.ComputeRevisionDigest(ctx, tc)
		if err != nil {
			return fmt.Errorf("compute revision digest: %w", err)
		}

		var watermark int64
		err = tc.QueryRow("SELECT coalesce(max(seq), 0) FROM global_sequence").Scan(&watermark)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("get max global sequence watermark: %w", err)
		}

		now := NowMicro()
		newID := core.NewUUID()
		newRun = &ModelRebuildRun{
			ID:                      newID,
			Kind:                    pred.Kind,
			BaseActiveVersion:       pred.BaseActiveVersion,
			TargetProjectionVersion: newTargetProjectionVersion,
			TargetPolicy:            pred.TargetPolicy,
			BaseRevisionDigest:      digest,
			FixedWatermark:          watermark,
			Status:                  "running",
			RequiresRebuild:         true,
			PredecessorRunID:        &predecessorRunID,
			CreatedAt:               now,
			UpdatedAt:               now,
		}

		insertQuery := `
			INSERT INTO model_rebuild_run (
				id, kind, base_active_version, target_projection_version, target_policy,
				base_revision_digest, fixed_watermark, status, requires_rebuild,
				predecessor_run_id, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, 'running', 1, ?, ?, ?)
		`
		_, err = tc.Exec(insertQuery,
			newRun.ID, newRun.Kind, newRun.BaseActiveVersion, newRun.TargetProjectionVersion, newRun.TargetPolicy,
			newRun.BaseRevisionDigest, newRun.FixedWatermark, predecessorRunID, now, now,
		)
		if err != nil {
			return fmt.Errorf("insert successor run: %w", err)
		}

		_, err = tc.Exec(
			"UPDATE projection_control SET run_id = ?, run_status = 'running', requires_rebuild = 1, fixed_watermark = ?, updated_at = ? WHERE id = 1",
			newRun.ID, watermark, now,
		)
		if err != nil {
			return fmt.Errorf("update projection_control for restart: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Execute replay
	replayErr := s.ExecuteReplay(ctx, newRun)
	pc, err := core.GetProjectionControl(s.db)
	if err != nil {
		return nil, err
	}

	nextAction := "none"
	if newRun.Status != "succeeded" {
		nextAction = "resume_or_abort"
	}

	res := &RebuildResult{
		RunID:           newRun.ID,
		Status:          newRun.Status,
		Mode:            pc.Mode,
		ActiveVersion:   pc.ActiveVersion,
		RequiresRebuild: pc.RequiresRebuild,
		NextAction:      nextAction,
	}

	if replayErr != nil {
		return res, replayErr
	}
	return res, nil
}
