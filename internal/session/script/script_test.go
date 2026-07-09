package script

import (
	"os"
	"strings"
	"testing"

	"github.com/nashory/agx/internal/agent"
)

func TestShellQuote(t *testing.T) {
	tests := map[string]string{
		"":                 "''",
		"plain":            "'plain'",
		"quote ' here":     "'quote '\"'\"' here'",
		"dollar $HOME \\n": "'dollar $HOME \\n'",
	}
	for input, want := range tests {
		if got := ShellQuote(input); got != want {
			t.Fatalf("ShellQuote(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRemoveCommandScriptDeletesTempScript(t *testing.T) {
	cmd, err := BuildTmuxCommand(agent.Agent{Name: "custom", Command: "true"}, "", "12345678-aaaa")
	if err != nil {
		t.Fatal(err)
	}
	path, ok := CommandScriptPath(cmd)
	if !ok {
		t.Fatalf("CommandScriptPath(%q) did not find script path", cmd)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}

	RemoveCommandScript(cmd)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("script still exists after cleanup: %v", err)
	}
}

func TestTaskExitStatusPathSanitizesTaskID(t *testing.T) {
	path := TaskExitStatusPath("task/id:1")
	if !strings.HasSuffix(path, "agx-task-task-id-1.status") {
		t.Fatalf("TaskExitStatusPath() = %q, want sanitized status filename", path)
	}
}
