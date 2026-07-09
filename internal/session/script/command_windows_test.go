//go:build windows

package script

import (
	"os"
	"strings"
	"testing"

	"github.com/nashory/agx/internal/agent"
)

func TestBuildCommandWritesSelfDeletingPowerShellScript(t *testing.T) {
	cmd, err := BuildTmuxCommandMode(
		agent.Agent{Name: "custom", Command: "my-agent.exe", Env: map[string]string{"MYVAR": "my'val"}},
		"do the thing",
		"12345678-win",
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	const prefix = `powershell.exe -NoProfile -ExecutionPolicy Bypass -File "`
	if !strings.HasPrefix(cmd, prefix) || !strings.HasSuffix(cmd, `"`) {
		t.Fatalf("unexpected command: %s", cmd)
	}
	path, ok := CommandScriptPath(cmd)
	if !ok {
		t.Fatalf("CommandScriptPath(%q) did not find script path", cmd)
	}
	defer os.Remove(path)
	if !strings.HasSuffix(path, ".ps1") {
		t.Fatalf("script path = %q, want .ps1 extension", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"Remove-Item -LiteralPath $PSCommandPath -Force",
		"Remove-Item -LiteralPath Env:CLAUDECODE",
		"Remove-Item -LiteralPath Env:CLAUDE_CODE_ENTRYPOINT",
		"$env:MYVAR = 'my''val'", // single quote doubled
		"& 'my-agent.exe'",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("script missing %q:\n%s", want, content)
		}
	}
}

func TestPowerShellQuote(t *testing.T) {
	cases := map[string]string{
		"":                     "''",
		"plain":                "'plain'",
		`C:\Users\a b\x`:       `'C:\Users\a b\x'`,
		"it's":                 "'it''s'",
		"$env:X and `backtick": "'$env:X and `backtick'",
	}
	for input, want := range cases {
		if got := powerShellQuote(input); got != want {
			t.Fatalf("powerShellQuote(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRemoveCommandScriptDeletesPowerShellScript(t *testing.T) {
	cmd, err := BuildTmuxCommandMode(agent.Agent{Name: "custom", Command: "my-agent.exe"}, "", "12345678-rm", false)
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
