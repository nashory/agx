//go:build windows

package processtree

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
)

func configurePlatform(*exec.Cmd) {}

// Terminate stops the root process and every child still attached to it.
// Claude's Windows launcher starts a native child that otherwise survives
// exec.CommandContext cancellation and keeps the session locked indefinitely.
func Terminate(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	treeErr := exec.Command("taskkill.exe", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run()
	if treeErr == nil {
		return nil
	}
	killErr := cmd.Process.Kill()
	if killErr == nil || errors.Is(killErr, os.ErrProcessDone) {
		return nil
	}
	return errors.Join(treeErr, killErr)
}
