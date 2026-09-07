//go:build !windows

package processtree

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configurePlatform(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// Terminate kills the process group created by Configure so agent subprocesses
// cannot outlive an interrupted or recovered turn.
func Terminate(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	groupErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if groupErr == nil || errors.Is(groupErr, syscall.ESRCH) {
		return nil
	}
	killErr := cmd.Process.Kill()
	if killErr == nil || errors.Is(killErr, os.ErrProcessDone) {
		return nil
	}
	return errors.Join(groupErr, killErr)
}
