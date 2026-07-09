//go:build !darwin && !linux && !windows

package main

import "os/exec"

func configureDetachedRuntimeCommand(cmd *exec.Cmd) {}
