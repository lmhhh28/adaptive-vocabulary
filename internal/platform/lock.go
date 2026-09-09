package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var (
	ErrServiceRunning = errors.New("SERVICE_RUNNING: process lock already acquired by another running instance")
)

type ProcessLock struct {
	file *os.File
	path string
}

func AcquireProcessLock(dataDir string) (*ProcessLock, error) {
	lockPath := filepath.Join(dataDir, "process.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open process lock file: %w", err)
	}

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrServiceRunning
		}
		return nil, fmt.Errorf("acquire process lock on %s: %w", lockPath, err)
	}

	return &ProcessLock{
		file: f,
		path: lockPath,
	}, nil
}

func (l *ProcessLock) Release() error {
	if l.file == nil {
		return nil
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	err := l.file.Close()
	l.file = nil
	return err
}
