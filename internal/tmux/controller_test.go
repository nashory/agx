package tmux

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInputKeyMapsCommonTerminalSequences(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantKey  string
		consumed int
	}{
		{name: "enter", input: "\r", wantKey: "Enter", consumed: 1},
		{name: "backspace", input: "\x7f", wantKey: "BSpace", consumed: 1},
		{name: "ctrl c", input: "\x03", wantKey: "C-c", consumed: 1},
		{name: "up", input: "\x1b[A", wantKey: "Up", consumed: 3},
		{name: "delete", input: "\x1b[3~", wantKey: "DC", consumed: 4},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key, consumed, ok := inputKey(test.input)
			if !ok {
				t.Fatal("inputKey returned ok=false")
			}
			if key != test.wantKey || consumed != test.consumed {
				t.Fatalf("inputKey() = (%q, %d), want (%q, %d)", key, consumed, test.wantKey, test.consumed)
			}
		})
	}
}

func TestInputLiteralPreservesUTF8(t *testing.T) {
	literal, consumed := inputLiteral("한글abc\r")
	if literal != "한글abc" {
		t.Fatalf("literal = %q, want %q", literal, "한글abc")
	}
	if consumed != len("한글abc") {
		t.Fatalf("consumed = %d, want %d", consumed, len("한글abc"))
	}
}

func TestSafeSessionNameAndTarget(t *testing.T) {
	tests := map[string]string{
		"AGX Project":       "agx-project",
		" Project -- Name ": "project-name",
		"***":               "project",
		"한글 Repo":           "한글-repo",
	}
	for input, want := range tests {
		if got := SafeSessionName(input); got != want {
			t.Fatalf("SafeSessionName(%q) = %q, want %q", input, got, want)
		}
	}
	if got, want := Target("session", "window"), "session:window"; got != want {
		t.Fatalf("Target() = %q, want %q", got, want)
	}
}

func TestControllerUsesTmuxCommandOutput(t *testing.T) {
	logPath := installFakeTmux(t, `#!/bin/sh
printf '%s\n' "$*" >> "$AGX_TMUX_LOG"
case "$*" in
  *"list-windows -t session -F #{window_name}"*) printf 'main\nworker\n' ;;
  *"list-windows -t session -F #{window_index}"*) printf '0\n1\n' ;;
  *"display-message -p -t session:main #{window_name}"*) printf 'main\n' ;;
  *"display-message -p -t session:main #{pane_current_command}"*) printf 'zsh\n' ;;
  *"capture-pane -t session:main -p -S -20"*) printf 'history\n' ;;
  *"capture-pane -t session:main -p"*) printf 'pane\n' ;;
esac
`)
	controller := NewController()
	if !controller.HasTmux() {
		t.Fatal("HasTmux() = false, want fake tmux on PATH")
	}
	if !controller.WindowExists("session:worker") {
		t.Fatal("WindowExists(session:worker) = false, want true")
	}
	if controller.WindowExists("session:missing") {
		t.Fatal("WindowExists(session:missing) = true, want false")
	}
	count, err := controller.WindowCount("session")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("WindowCount() = %d, want 2", count)
	}
	name, err := controller.WindowName("session:main")
	if err != nil {
		t.Fatal(err)
	}
	if name != "main" {
		t.Fatalf("WindowName() = %q, want main", name)
	}
	command, err := controller.PaneCurrentCommand("session:main")
	if err != nil {
		t.Fatal(err)
	}
	if command != "zsh" {
		t.Fatalf("PaneCurrentCommand() = %q, want zsh", command)
	}
	if pane, err := controller.CapturePane("session:main"); err != nil || pane != "pane\n" {
		t.Fatalf("CapturePane() = (%q, %v), want pane output", pane, err)
	}
	if history, err := controller.CapturePaneWithHistory("session:main", 20); err != nil || history != "history\n" {
		t.Fatalf("CapturePaneWithHistory() = (%q, %v), want history output", history, err)
	}

	log := readLog(t, logPath)
	for _, want := range []string{
		"-L agx list-windows -t session -F #{window_name}",
		"-L agx list-windows -t session -F #{window_index}",
		"-L agx display-message -p -t session:main #{window_name}",
		"-L agx capture-pane -t session:main -p -S -20",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("tmux log missing %q:\n%s", want, log)
		}
	}
}

func TestControllerSendsInputAndWindowCommands(t *testing.T) {
	logPath := installFakeTmux(t, `#!/bin/sh
printf '%s\n' "$*" >> "$AGX_TMUX_LOG"
`)
	controller := NewController()

	if err := controller.SendInput("session:main", "ab\r\t\x7f\x03\x1b[Acd"); err != nil {
		t.Fatal(err)
	}
	if err := controller.ResizeWindow("session:main", 120, 40); err != nil {
		t.Fatal(err)
	}
	if err := controller.ResizeWindow("session:main", 0, 40); err != nil {
		t.Fatal(err)
	}
	if err := controller.SetOption("status", "off"); err != nil {
		t.Fatal(err)
	}
	if err := controller.CreateWindow("session", "worker", "/tmp", "echo hi"); err != nil {
		t.Fatal(err)
	}
	if err := controller.RenameWindow("session:worker", "renamed"); err != nil {
		t.Fatal(err)
	}
	if err := controller.PipePane("session:main", "cat >/tmp/out"); err != nil {
		t.Fatal(err)
	}
	if err := controller.ReplacePipePane("session:main", "cat >/tmp/next"); err != nil {
		t.Fatal(err)
	}
	if err := controller.StopPipePane("session:main"); err != nil {
		t.Fatal(err)
	}
	if err := controller.KillWindow("session:worker"); err != nil {
		t.Fatal(err)
	}
	if err := controller.KillSession("session"); err != nil {
		t.Fatal(err)
	}
	if err := controller.KillServer(); err != nil {
		t.Fatal(err)
	}

	log := readLog(t, logPath)
	for _, want := range []string{
		"-L agx send-keys -t session:main -l -- ab",
		"-L agx send-keys -t session:main Enter",
		"-L agx send-keys -t session:main Tab",
		"-L agx send-keys -t session:main BSpace",
		"-L agx send-keys -t session:main C-c",
		"-L agx send-keys -t session:main Up",
		"-L agx send-keys -t session:main -l -- cd",
		"-L agx resize-window -t session:main -x 120 -y 40",
		"-L agx set-option -g status off",
		"-L agx new-window -d -t session: -n worker -c /tmp echo hi",
		"-L agx rename-window -t session:worker renamed",
		"-L agx pipe-pane -o -t session:main cat >/tmp/out",
		"-L agx pipe-pane -t session:main cat >/tmp/next",
		"-L agx pipe-pane -t session:main",
		"-L agx kill-window -t session:worker",
		"-L agx kill-session -t session",
		"-L agx kill-server",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("tmux log missing %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "-x 0") {
		t.Fatalf("ResizeWindow with invalid size should not call tmux:\n%s", log)
	}
}

func installFakeTmux(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake tmux shell script requires a Unix shell")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "tmux.log")
	path := filepath.Join(dir, "tmux")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGX_TMUX_LOG", logPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
