//go:build windows

package script

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nashory/agx/internal/agent"
)

// buildRunScript builds the PowerShell script that runs the agent under ConPTY.
// It self-deletes, scrubs Claude entrypoint env vars, sets agent env, and invokes
// the agent. ConPTY already provides a real pseudo-console, so no script(1)-style
// TTY wrapper is needed, and exit-status recording is omitted because the ConPTY
// backend derives status from output activity and process liveness, not a status
// file (statusPath is accepted for signature parity with the POSIX builder).
func buildRunScript(ag agent.Agent, prompt, statusPath string, allMighty bool) (string, error) {
	argv := ag.BuildRunCommandMode(prompt, allMighty)
	if len(argv) == 0 {
		return "", fmt.Errorf("agent %q produced an empty command", ag.Name)
	}
	var b strings.Builder
	b.WriteString("$ErrorActionPreference = 'Stop'\n")
	b.WriteString("Remove-Item -LiteralPath $PSCommandPath -Force -ErrorAction SilentlyContinue\n")
	b.WriteString("Remove-Item -LiteralPath Env:CLAUDECODE -ErrorAction SilentlyContinue\n")
	b.WriteString("Remove-Item -LiteralPath Env:CLAUDE_CODE_ENTRYPOINT -ErrorAction SilentlyContinue\n")
	for key, value := range ag.Env {
		if !isShellIdentifier(key) {
			return "", fmt.Errorf("invalid environment variable name %q", key)
		}
		b.WriteString("$env:" + key + " = " + powerShellQuote(value) + "\n")
	}
	b.WriteString("& " + powerShellJoin(argv) + "\n")
	return b.String(), nil
}

// wrapScriptCommand returns the command line that runs the PowerShell script. It
// is passed to CreateProcess by the ConPTY backend, so PowerShell hosts the agent.
func wrapScriptCommand(path string) string {
	return `powershell.exe -NoProfile -ExecutionPolicy Bypass -File "` + path + `"`
}

func scriptCommandPath(command string) (string, bool) {
	const prefix = `powershell.exe -NoProfile -ExecutionPolicy Bypass -File "`
	const suffix = `"`
	if !strings.HasPrefix(command, prefix) || !strings.HasSuffix(command, suffix) {
		return "", false
	}
	path := strings.TrimSuffix(strings.TrimPrefix(command, prefix), suffix)
	if path == "" {
		return "", false
	}
	return filepath.Clean(path), true
}

func scriptFileExtension() string {
	return ".ps1"
}

// powerShellQuote wraps s in a single-quoted PowerShell literal, escaping embedded
// single quotes by doubling them. Single-quoted strings do not expand variables or
// interpret backslashes, so Windows paths are safe.
func powerShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func powerShellJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, arg := range argv {
		parts[i] = powerShellQuote(arg)
	}
	return strings.Join(parts, " ")
}
