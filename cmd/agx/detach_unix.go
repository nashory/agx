//go:build darwin || linux

package main

import (
	"os/exec"
	"syscall"
)

func configureDetachedRuntimeCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
