//go:build windows

package runtime

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// Windows byte-range locks are mandatory: an exclusive lock blocks other handles
// from reading the locked bytes, unlike advisory Unix flock. The lock file's
// diagnostic contents must stay readable while the lock is held (callers read it
// to report the current owner), so the lock is taken on a single dedicated byte
// at a high offset that never overlaps the content at [0, N). This mirrors the
// classic SQLite "lock byte" technique.
const (
	lockRegionLength     = 1
	lockRegionOffsetHigh = 0x8000_0000
)

func lockRegionOverlapped() *windows.Overlapped {
	return &windows.Overlapped{OffsetHigh: lockRegionOffsetHigh}
}

// lockRuntimeFile takes an exclusive, non-blocking lock on the dedicated lock
// byte. A second handle to the same file (in this or another process) fails
// immediately with ERROR_LOCK_VIOLATION, mirroring the Unix flock behavior.
func lockRuntimeFile(file *os.File) error {
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, lockRegionLength, 0, lockRegionOverlapped(),
	)
}

func unlockRuntimeFile(file *os.File) error {
	return windows.UnlockFileEx(
		windows.Handle(file.Fd()),
		0, lockRegionLength, 0, lockRegionOverlapped(),
	)
}

// isRuntimeLockHeldError reports whether err means another handle already holds
// the lock, as opposed to a genuine failure.
func isRuntimeLockHeldError(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
