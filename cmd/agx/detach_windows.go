//go:build windows

package main

import "os/exec"

func configureDetachedRuntimeCommand(cmd *exec.Cmd) {
	// Native Windows launch is intentionally unsupported; this keeps the CLI
	// buildable while WSL2 uses the Unix implementation.
}
