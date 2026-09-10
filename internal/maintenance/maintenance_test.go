package maintenance_test

import (
	"context"
	"testing"

	"adaptive-vocabulary/internal/maintenance"
	"adaptive-vocabulary/internal/platform"
)

func setupTestDB(t *testing.T) (*platform.DB, *maintenance.Service, func()) {
	t.Helper()
	dir := t.TempDir()
	db, err := platform.OpenDB(dir)
	if err != nil {
		t.Fatalf("OpenDB failed: %v", err)
	}

	_, err = platform.RunMigrations(db.DB)
	if err != nil {
		_ = db.Close()
		t.Fatalf("RunMigrations failed: %v", err)
	}

	svc := maintenance.NewService(db.DB)
	cleanup := func() {
		_ = db.Close()
	}
	return db, svc, cleanup
}

func TestMaintenanceStatus_Initial(t *testing.T) {
	_, svc, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	state, err := svc.GetMaintenanceState(ctx)
	if err != nil {
		t.Fatalf("GetMaintenanceState failed: %v", err)
	}

	if state.Mode != "active" {
		t.Errorf("expected mode active, got %s", state.Mode)
	}
	if state.RequiresRebuild {
		t.Errorf("expected requires_rebuild false, got true")
	}
}

func TestRebuild_PolicyOnly_Abort(t *testing.T) {
	_, svc, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Start policy_only rebuild
	run, err := svc.StartRebuildRun(ctx, "policy_only", "policy-v2", "v_test_policy")
	if err != nil {
		t.Fatalf("StartRebuildRun failed: %v", err)
	}

	state, err := svc.GetMaintenanceState(ctx)
	if err != nil {
		t.Fatalf("GetMaintenanceState failed: %v", err)
	}
	if state.Mode != "rebuilding" {
		t.Errorf("expected mode rebuilding, got %s", state.Mode)
	}

	// Abort: since digest has not changed, policy_only abort safely releases maintenance
	res, err := svc.Abort(ctx, run.ID)
	if err != nil {
		t.Fatalf("Abort failed: %v", err)
	}
	if res.Status != "aborted" {
		t.Errorf("expected status aborted, got %s", res.Status)
	}
	if res.Mode != "active" {
		t.Errorf("expected mode active, got %s", res.Mode)
	}
	if res.RequiresRebuild {
		t.Errorf("expected requires_rebuild false, got true")
	}

	// Verify idempotency
	res2, err := svc.Abort(ctx, run.ID)
	if err != nil {
		t.Fatalf("Abort idempotent call failed: %v", err)
	}
	if res2.Status != "aborted" {
		t.Errorf("expected idempotent status aborted, got %s", res2.Status)
	}
}

func TestRebuild_EvidenceRevision_Abort_And_Restart(t *testing.T) {
	_, svc, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Start evidence_revision rebuild
	run, err := svc.StartRebuildRun(ctx, "evidence_revision", "policy-v2", "v_test_ev")
	if err != nil {
		t.Fatalf("StartRebuildRun failed: %v", err)
	}

	// Abort: evidence_revision abort must result in aborted_maintenance and keep rebuilding gate
	res, err := svc.Abort(ctx, run.ID)
	if err != nil {
		t.Fatalf("Abort failed: %v", err)
	}
	if res.Status != "aborted_maintenance" {
		t.Errorf("expected status aborted_maintenance, got %s", res.Status)
	}
	if res.Mode != "rebuilding" {
		t.Errorf("expected mode rebuilding, got %s", res.Mode)
	}
	if !res.RequiresRebuild {
		t.Errorf("expected requires_rebuild true, got false")
	}
	if res.NextAction != "restart" {
		t.Errorf("expected next_action restart, got %s", res.NextAction)
	}

	// Restart with new projection version
	newProj := "v_test_restart"
	restartRes, err := svc.Restart(ctx, run.ID, newProj)
	if err != nil {
		t.Fatalf("Restart failed: %v", err)
	}
	if restartRes.Status != "succeeded" {
		t.Errorf("expected restart status succeeded, got %s", restartRes.Status)
	}
	if restartRes.Mode != "active" {
		t.Errorf("expected restart mode active, got %s", restartRes.Mode)
	}
	if restartRes.ActiveVersion != newProj {
		t.Errorf("expected active_version %s, got %s", newProj, restartRes.ActiveVersion)
	}
	if restartRes.RequiresRebuild {
		t.Errorf("expected requires_rebuild false, got true")
	}

	// Idempotent restart with same projection version
	restartRes2, err := svc.Restart(ctx, run.ID, newProj)
	if err != nil {
		t.Fatalf("Restart idempotent call failed: %v", err)
	}
	if restartRes2.Status != "succeeded" {
		t.Errorf("expected idempotent status succeeded, got %s", restartRes2.Status)
	}

	// Conflicting restart with different projection version
	_, err = svc.Restart(ctx, run.ID, "v_conflicting_version")
	if err == nil {
		t.Errorf("expected error for conflicting restart, got nil")
	}
}

func TestRebuildAbortInvalidRunID(t *testing.T) {
	_, svc, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Aborting non-existent run ID should return error
	_, err := svc.Abort(ctx, "non-existent-run-id")
	if err == nil {
		t.Errorf("expected error aborting non-existent run ID, got nil")
	}
}

