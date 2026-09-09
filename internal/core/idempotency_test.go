package core_test

import (
	"context"
	"testing"

	"adaptive-vocabulary/internal/core"
)

func TestIdempotency_CheckAndSave(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	digest, err := core.ComputeRequestDigest([]byte(`{"foo": "bar", "num": 1}`))
	if err != nil {
		t.Fatalf("ComputeRequestDigest failed: %v", err)
	}

	// 1. Initial check: not found
	err = core.RunInTx(ctx, db.DB, func(tc *core.TxnContext) error {
		rec, err := core.CheckIdempotency(tc, "POST", "test_scope", "key-123", digest)
		if err != nil {
			return err
		}
		if rec != nil {
			t.Errorf("expected nil record initially")
		}

		// Save record
		return core.SaveIdempotencyRecord(tc, &core.IdempotencyRecord{
			Operation:     "POST",
			ResourceScope: "test_scope",
			Key:           "key-123",
			RequestDigest: digest,
			Status:        "success",
			ResponseCode:  200,
			ResponseBody:  `{"result":"ok"}`,
			CreatedAt:     1000,
		})
	})
	if err != nil {
		t.Fatalf("save idempotency failed: %v", err)
	}

	// 2. Second check with same digest: match
	err = core.RunInTx(ctx, db.DB, func(tc *core.TxnContext) error {
		rec, err := core.CheckIdempotency(tc, "POST", "test_scope", "key-123", digest)
		if err != nil {
			return err
		}
		if rec == nil || rec.ResponseCode != 200 {
			t.Errorf("expected matched record with 200, got %+v", rec)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("recheck idempotency failed: %v", err)
	}

	// 3. Third check with differing digest: conflict
	err = core.RunInTx(ctx, db.DB, func(tc *core.TxnContext) error {
		_, err := core.CheckIdempotency(tc, "POST", "test_scope", "key-123", "different_digest")
		if err != core.ErrIdempotencyConflict {
			t.Errorf("expected ErrIdempotencyConflict, got %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("conflict check failed: %v", err)
	}
}
