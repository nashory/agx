package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Lock represents the process-wide runtime daemon lock file. The file remains
// open while the lock is held so the advisory flock is retained by the process.
type Lock struct {
	file *os.File
	path string
}

// AcquireLock takes an exclusive non-blocking flock at path and records the
// current process metadata for diagnostics. A second runtime process receives an
// error containing the existing lock file contents.
func AcquireLock(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create runtime lock dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open runtime lock: %w", err)
	}
	if err := lockRuntimeFile(file); err != nil {
		existing, _ := os.ReadFile(path)
		_ = file.Close()
		if len(existing) == 0 {
			return nil, fmt.Errorf("acquire runtime lock: %w", err)
		}
		return nil, fmt.Errorf("agx runtime already running: %s", string(existing))
	}
	if err := file.Truncate(0); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("truncate runtime lock: %w", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("seek runtime lock: %w", err)
	}
	if _, err := fmt.Fprintf(file, "pid=%d started=%s\n", os.Getpid(), time.Now().Format(time.RFC3339)); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write runtime lock: %w", err)
	}
	return &Lock{file: file, path: path}, nil
}

// Release unlocks and closes the lock file. It is idempotent for nil or already
// released locks.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unlockRuntimeFile(l.file)
	if closeErr := l.file.Close(); err == nil {
		err = closeErr
	}
	l.file = nil
	return err
}

// Path returns the lock file path, or an empty string for a nil lock.
func (l *Lock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// LockHeld reports whether another process currently holds the runtime lock.
// It does not modify the lock file contents when the lock is available.
func LockHeld(path string) (bool, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if errorsIsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open runtime lock: %w", err)
	}
	defer func() { _ = file.Close() }()
	if err := lockRuntimeFile(file); err != nil {
		if isRuntimeLockHeldError(err) {
			return true, nil
		}
		return false, fmt.Errorf("check runtime lock: %w", err)
	}
	_ = unlockRuntimeFile(file)
	return false, nil
}

// LockOwnerPID reads the diagnostic pid written by AcquireLock.
func LockOwnerPID(path string) (int, bool, string, error) {
	data, err := os.ReadFile(path)
	if errorsIsNotExist(err) {
		return 0, false, "", nil
	}
	if err != nil {
		return 0, false, "", fmt.Errorf("read runtime lock: %w", err)
	}
	raw := string(data)
	for _, field := range strings.Fields(raw) {
		value, ok := strings.CutPrefix(field, "pid=")
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(value)
		if err != nil || pid <= 0 {
			return 0, false, raw, nil
		}
		return pid, true, raw, nil
	}
	return 0, false, raw, nil
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}
