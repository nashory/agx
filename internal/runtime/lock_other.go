//go:build !darwin && !linux && !windows

package runtime

import (
	"fmt"
	"os"
)

func lockRuntimeFile(file *os.File) error {
	return fmt.Errorf("runtime locking is not supported on this platform")
}

func unlockRuntimeFile(file *os.File) error {
	return nil
}

func isRuntimeLockHeldError(err error) bool {
	return false
}
