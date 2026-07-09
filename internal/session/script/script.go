package script

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nashory/agx/internal/agent"
)

func BuildTmuxCommand(ag agent.Agent, prompt, taskID string) (string, error) {
	return BuildTmuxCommandMode(ag, prompt, taskID, true)
}

// BuildTmuxCommandMode writes a temporary run script for the agent and returns the
// command line that executes it. The script contents and wrapping command are
// platform-specific: a POSIX shell script on Unix (run under tmux) and a
// PowerShell script on native Windows (run under ConPTY). See buildRunScript and
// wrapScriptCommand in the platform files.
func BuildTmuxCommandMode(ag agent.Agent, prompt, taskID string, allMighty bool) (string, error) {
	statusPath := TaskExitStatusPath(taskID)
	_ = os.Remove(statusPath)
	content, err := buildRunScript(ag, prompt, statusPath, allMighty)
	if err != nil {
		return "", err
	}
	path, err := WriteTempScript(taskID, content)
	if err != nil {
		return "", err
	}
	return wrapScriptCommand(path), nil
}

func RemoveCommandScript(command string) {
	path, ok := CommandScriptPath(command)
	if !ok {
		return
	}
	_ = os.Remove(path)
}

// CommandScriptPath extracts the temporary script path from a command produced by
// BuildTmuxCommandMode, using the platform-specific wrapping format.
func CommandScriptPath(command string) (string, bool) {
	return scriptCommandPath(command)
}

func WriteTempScript(taskID, content string) (string, error) {
	prefix := "agx-run-"
	if len(taskID) >= 8 {
		prefix += taskID[:8] + "-"
	}
	f, err := os.CreateTemp("", prefix+"*"+scriptFileExtension())
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
