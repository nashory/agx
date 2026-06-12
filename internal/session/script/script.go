package script

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nashory/agx/internal/agent"
)

func BuildTmuxCommand(ag agent.Agent, prompt, taskID string) (string, error) {
	return BuildTmuxCommandMode(ag, prompt, taskID, true)
}

func BuildTmuxCommandMode(ag agent.Agent, prompt, taskID string, allMighty bool) (string, error) {
	argv := ag.BuildRunCommandMode(prompt, allMighty)
	statusPath := TaskExitStatusPath(taskID)
	_ = os.Remove(statusPath)
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

	path, err := WriteTempScript(taskID, content)
	if err != nil {
		return "", err
	}
	return "sh " + ShellQuote(path) + "; exec ${SHELL:-/bin/sh}", nil
}

func RemoveCommandScript(command string) {
	path, ok := CommandScriptPath(command)
	if !ok {
		return
	}
	_ = os.Remove(path)
}

func CommandScriptPath(command string) (string, bool) {
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

func WriteTempScript(taskID, content string) (string, error) {
	prefix := "agx-run-"
	if len(taskID) >= 8 {
		prefix += taskID[:8] + "-"
	}
	f, err := os.CreateTemp("", prefix+"*.sh")
	if err != nil {
		return "", err
	}
	path := f.Name()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return filepath.Clean(path), nil
}

func TaskExitStatusPath(taskID string) string {
	name := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, taskID)
	return filepath.Join(os.TempDir(), "agx-task-"+name+".status")
}

func ReadTaskExitStatus(taskID string) (int, bool) {
	data, err := os.ReadFile(TaskExitStatusPath(taskID))
	if err != nil {
		return 0, false
	}
	code := strings.TrimSpace(string(data))
	value, err := strconv.Atoi(code)
	if err != nil {
		return 1, true
	}
	return value, true
}

func HasTaskExitStatus(taskID string) bool {
	_, err := os.Stat(TaskExitStatusPath(taskID))
	return err == nil
}

func ShellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, ShellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func isShellIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' {
				continue
			}
			return false
		}
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}
