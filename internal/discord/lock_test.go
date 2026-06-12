package discord

import (
	"os"
	"strings"
	"testing"
)

func TestAcquireLockRejectsSecondOwnerAndRecordsMode(t *testing.T) {
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	lock, err := AcquireLock("runtime")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	if lock.Path() == "" {
		t.Fatal("Path() = empty, want lock path")
	}
	data, err := os.ReadFile(lock.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "mode=runtime") {
		t.Fatalf("lock file = %q, want mode recorded", string(data))
	}
	if _, err := AcquireLock("desktop"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second AcquireLock error = %v, want already running", err)
	}
}

func TestAcquireLockCanReacquireAfterRelease(t *testing.T) {
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	lock, err := AcquireLock("runtime")
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	next, err := AcquireLock("desktop")
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Release(); err != nil {
		t.Fatal(err)
	}
	if (*Lock)(nil).Path() != "" {
		t.Fatal("nil lock Path() should be empty")
	}
	if err := (*Lock)(nil).Release(); err != nil {
		t.Fatalf("nil lock Release() error = %v, want nil", err)
	}
}
