//go:build darwin || linux

package runtime

import (
	"os"
	"syscall"
)

func lockRuntimeFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlockRuntimeFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

func isRuntimeLockHeldError(err error) bool {
	return err == syscall.EWOULDBLOCK || err == syscall.EAGAIN
}
