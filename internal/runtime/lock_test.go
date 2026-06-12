package runtime

import (
	"path/filepath"
	"testing"
)

func TestAcquireLockRejectsSecondOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.lock")
	lock, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock() error = %v", err)
	}
	defer func() { _ = lock.Release() }()
	if _, err := AcquireLock(path); err == nil {
		t.Fatal("second AcquireLock() error = nil, want already running error")
	}
}

func TestAcquireLockCanReacquireAfterRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.lock")
	lock, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock() error = %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	next, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock() after Release() error = %v", err)
	}
	_ = next.Release()
}

func TestLockHeldAndOwnerPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.lock")
	lock, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock() error = %v", err)
	}
	held, err := LockHeld(path)
	if err != nil {
		t.Fatalf("LockHeld() error = %v", err)
	}
	if !held {
		t.Fatal("LockHeld() = false, want true")
	}
	pid, ok, raw, err := LockOwnerPID(path)
	if err != nil {
		t.Fatalf("LockOwnerPID() error = %v", err)
	}
	if !ok || pid <= 0 || raw == "" {
		t.Fatalf("LockOwnerPID() = (%d, %t, %q), want pid", pid, ok, raw)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	held, err = LockHeld(path)
	if err != nil {
		t.Fatalf("LockHeld() after release error = %v", err)
	}
	if held {
		t.Fatal("LockHeld() after release = true, want false")
	}
}
