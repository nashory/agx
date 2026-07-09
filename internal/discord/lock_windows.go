//go:build windows

package discord

import (
	"fmt"
	"os"
)

func lockFile(file *os.File) error {
	return fmt.Errorf("discord bridge locking is not supported on native Windows; use WSL2 Ubuntu")
}

func unlockFile(file *os.File) error {
	return nil
}
