//go:build !windows

package script

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nashory/agx/internal/agent"
)

// buildRunScript builds the POSIX shell script that runs the agent under tmux. It
// self-deletes, records the agent exit status (except for interactive
// injected-prompt agents), scrubs Claude entrypoint env vars, exports agent env,
// and execs the agent (under script(1) for a real TTY when a prompt is injected).
func buildRunScript(ag agent.Agent, prompt, statusPath string, allMighty bool) (string, error) {
	argv := ag.BuildRunCommandMode(prompt, allMighty)
	interactiveInjectedPrompt := ag.ShouldInjectInitialPrompt()
	content := "#!/bin/sh\n"
	content += "rm -f \"$0\"\n"
	if !interactiveInjectedPrompt {
		content += "status_file=" + ShellQuote(statusPath) + "\n"
		content += "record_exit() {\n"
		content += "  code=$?\n"
		content += "  printf '%s\\n' \"$code\" > \"$status_file\"\n"
		content += "  exit \"$code\"\n"
		content += "}\n"
		content += "trap record_exit EXIT\n"
	}
	content += "unset CLAUDECODE CLAUDE_CODE_ENTRYPOINT\n"
	for key, value := range ag.Env {
		if !isShellIdentifier(key) {
			return "", fmt.Errorf("invalid environment variable name %q", key)
		}
		content += "export " + key + "=" + ShellQuote(value) + "\n"
	}
	if interactiveInjectedPrompt {
		content += "script -q /dev/null " + ShellJoin(argv) + "\n"
	} else {
		content += ShellJoin(argv) + "\n"
	}
	return content, nil
}

// wrapScriptCommand runs the script and then drops into an interactive shell so
// the tmux window stays alive after the agent exits.
func wrapScriptCommand(path string) string {
	return "sh " + ShellQuote(path) + "; exec ${SHELL:-/bin/sh}"
}

func scriptCommandPath(command string) (string, bool) {
	const prefix = "sh '"
	const suffix = "'; exec ${SHELL:-/bin/sh}"
	if !strings.HasPrefix(command, prefix) || !strings.HasSuffix(command, suffix) {
		return "", false
	}
	path := strings.TrimSuffix(strings.TrimPrefix(command, prefix), suffix)
	if strings.Contains(path, "'") {
		return "", false
	}
	return filepath.Clean(path), true
}

func scriptFileExtension() string {
	return ".sh"
}
