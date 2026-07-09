//go:build windows

package runtime

import (
	"fmt"
	"os"
)

func lockRuntimeFile(file *os.File) error {
	return fmt.Errorf("runtime locking is not supported on native Windows; use WSL2 Ubuntu")
}

func unlockRuntimeFile(file *os.File) error {
	return nil
}

func isRuntimeLockHeldError(err error) bool {
	return false
}
