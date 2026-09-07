package processtree

import (
	"os/exec"
	"time"
)

const commandWaitDelay = 2 * time.Second

// Configure makes context cancellation terminate the command and its children,
// then bounds how long Wait may remain blocked on inherited pipes.
func Configure(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	Prepare(cmd)
	cmd.Cancel = func() error {
		return Terminate(cmd)
	}
	cmd.WaitDelay = commandWaitDelay
}

// Prepare configures a command so Terminate can stop its full process tree.
// It is useful for long-lived commands that are not tied to a request context.
func Prepare(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	configurePlatform(cmd)
}
