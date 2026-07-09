//go:build windows

package discord

import (
	"os"

	"golang.org/x/sys/windows"
)

// Windows byte-range locks are mandatory: an exclusive lock blocks other handles
// from reading the locked bytes, unlike advisory Unix flock. The lock file's
// diagnostic contents must stay readable while the lock is held (a second
// process reads it to report the current owner), so the lock is taken on a
// single dedicated byte at a high offset that never overlaps the content at
// [0, N). This mirrors the classic SQLite "lock byte" technique.
const (
	lockRegionLength     = 1
	lockRegionOffsetHigh = 0x8000_0000
)

func lockRegionOverlapped() *windows.Overlapped {
	return &windows.Overlapped{OffsetHigh: lockRegionOffsetHigh}
}

// lockFile takes an exclusive, non-blocking lock on the dedicated lock byte. A
// second handle to the same file fails immediately, mirroring the Unix flock
// behavior used on macOS and Linux.
func lockFile(file *os.File) error {
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, lockRegionLength, 0, lockRegionOverlapped(),
	)
}

func unlockFile(file *os.File) error {
	return windows.UnlockFileEx(
		windows.Handle(file.Fd()),
		0, lockRegionLength, 0, lockRegionOverlapped(),
	)
}
