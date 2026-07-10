//go:build !windows

package script

import (
	"os"
	osexec "os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/nashory/agx/internal/agent"
)

func TestBuildTmuxCommandWritesPrivateSelfDeletingScript(t *testing.T) {
	cmd, err := BuildTmuxCommand(agent.Agent{Name: "claude", Command: "missing-claude-for-test"}, "quote ' and $HOME", "12345678-aaaa")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd, "sh '") || !strings.Contains(cmd, "; exec ${SHELL:-/bin/sh}") {
		t.Fatalf("unexpected tmux command: %s", cmd)
	}
	start := strings.Index(cmd, "'")
	end := strings.Index(cmd[start+1:], "'")
	if start < 0 || end < 0 {
		t.Fatalf("could not extract script path from %q", cmd)
	}
	path := cmd[start+1 : start+1+end]
	defer os.Remove(path)
	extracted, ok := CommandScriptPath(cmd)
	if !ok {
		t.Fatalf("CommandScriptPath(%q) did not find script path", cmd)
	}
	if extracted != path {
		t.Fatalf("CommandScriptPath() = %q, want %q", extracted, path)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("script mode = %o, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"rm -f \"$0\"",
		"record_exit()",
		"unset CLAUDECODE CLAUDE_CODE_ENTRYPOINT",
		"'missing-claude-for-test' '--dangerously-skip-permissions' 'quote '\"'\"' and $HOME'",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("script missing %q:\n%s", want, content)
		}
	}
}

func TestBuildTmuxCommandOmitsPromptForWrappedClaude(t *testing.T) {
	path := t.TempDir() + "/claude"
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec /usr/local/bin/claude_code/claude \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd, err := BuildTmuxCommand(agent.Agent{Name: "claude", Command: path}, "implement auth", "12345678-wrap")
	if err != nil {
		t.Fatal(err)
	}
	scriptPath, ok := CommandScriptPath(cmd)
	if !ok {
		t.Fatalf("CommandScriptPath(%q) did not find script path", cmd)
	}
	defer os.Remove(scriptPath)
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Contains(content, "implement auth") {
		t.Fatalf("wrapped Claude command should not include initial prompt argv:\n%s", content)
	}
	for _, unexpected := range []string{"status_file=", "record_exit()", "trap record_exit EXIT"} {
		if strings.Contains(content, unexpected) {
			t.Fatalf("wrapped Claude command should not record wrapper exit status %q:\n%s", unexpected, content)
		}
	}
	sandboxFlag := "--dangerously-disable-osx-sandbox"
	if runtime.GOOS == "linux" {
		sandboxFlag = "--dangerously-disable-linux-sandbox"
	}
	if !strings.Contains(content, "script -q /dev/null '"+path+"' '"+sandboxFlag+"' '--dangerously-skip-permissions'") {
		t.Fatalf("script missing wrapped Claude command:\n%s", content)
	}
}

func TestBuildTmuxCommandRecordsExitStatus(t *testing.T) {
	taskID := "12345678-exit-status"
	statusPath := TaskExitStatusPath(taskID)
	t.Cleanup(func() { _ = os.Remove(statusPath) })

	cmd, err := BuildTmuxCommandMode(agent.Agent{Name: "custom", Command: "sh", Args: []string{"-c", "exit 7"}}, "", taskID, false)
	if err != nil {
		t.Fatal(err)
	}
	path, ok := CommandScriptPath(cmd)
	if !ok {
		t.Fatalf("CommandScriptPath(%q) did not find script path", cmd)
	}

	if err := osexec.Command("sh", path).Run(); err == nil {
		t.Fatal("script exit error = nil, want non-zero exit")
	}
	code, ok := ReadTaskExitStatus(taskID)
	if !ok || code != 7 {
		t.Fatalf("ReadTaskExitStatus() = (%d, %v), want (7, true)", code, ok)
	}
}
