//go:build !darwin && !linux && !windows

package discord

import (
	"fmt"
	"os"
)

func lockFile(file *os.File) error {
	return fmt.Errorf("discord bridge locking is not supported on this platform")
}

func unlockFile(file *os.File) error {
	return nil
}
