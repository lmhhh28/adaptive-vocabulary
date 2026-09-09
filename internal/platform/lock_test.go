package platform_test

import (
	"errors"
	"testing"

	"adaptive-vocabulary/internal/platform"
)

func TestProcessLock(t *testing.T) {
	dir := t.TempDir()

	// 1. Acquire first lock
	lock1, err := platform.AcquireProcessLock(dir)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer func() { _ = lock1.Release() }()

	// 2. Second acquire should return ErrServiceRunning
	lock2, err := platform.AcquireProcessLock(dir)
	if !errors.Is(err, platform.ErrServiceRunning) {
		if lock2 != nil {
			_ = lock2.Release()
		}
		t.Fatalf("expected ErrServiceRunning, got err=%v", err)
	}

	// 3. Release first lock
	if err := lock1.Release(); err != nil {
		t.Fatalf("release lock1 failed: %v", err)
	}

	// 4. Now second acquire should succeed
	lock3, err := platform.AcquireProcessLock(dir)
	if err != nil {
		t.Fatalf("acquire lock3 after release failed: %v", err)
	}
	_ = lock3.Release()
}
