package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agxruntime "github.com/nashory/agx/internal/runtime"
	"github.com/spf13/cobra"
)

func TestPrintRuntimeServiceStatus(t *testing.T) {
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	printRuntimeServiceStatus(cmd, agxruntime.RuntimeServiceStatus{
		Manager:   "systemd",
		PathLabel: "systemd unit",
		Path:      "/home/agx/.config/systemd/user/dev.agx.runtime.service",
		State:     "missing",
		Detail:    "systemctl not found",
	})
	text := out.String()
	for _, want := range []string{
		"systemd unit: /home/agx/.config/systemd/user/dev.agx.runtime.service (missing)",
		"systemd detail: systemctl not found",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("runtime service status output missing %q:\n%s", want, text)
		}
	}
}

func TestPrintRuntimeServiceStatusWithoutPath(t *testing.T) {
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	printRuntimeServiceStatus(cmd, agxruntime.RuntimeServiceStatus{
		State: "unsupported",
	})
	if got, want := out.String(), "runtime service: unsupported\n"; got != want {
		t.Fatalf("runtime service status output = %q, want %q", got, want)
	}
}

func TestRuntimeBackedInvocationCoversStatefulCommands(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{args: []string{"agx", "run"}, want: true},
		{args: []string{"agx", "ps"}, want: true},
		{args: []string{"agx", "logs"}, want: true},
		{args: []string{"agx", "send"}, want: true},
		{args: []string{"agx", "stop"}, want: true},
		{args: []string{"agx", "interrupt"}, want: true},
		{args: []string{"agx", "attach"}, want: true},
		{args: []string{"agx", "chat", "sync"}, want: true},
		{args: []string{"agx", "task", "create"}, want: true},
		{args: []string{"agx", "task", "list"}, want: true},
		{args: []string{"agx", "task", "show"}, want: true},
		{args: []string{"agx", "task", "edit"}, want: true},
		{args: []string{"agx", "task", "delete"}, want: true},
		{args: []string{"agx", "project", "init"}, want: true},
		{args: []string{"agx", "project", "list"}, want: true},
		{args: []string{"agx", "project", "config"}, want: true},
		{args: []string{"agx", "project", "delete"}, want: true},
		{args: []string{"agx", "agent", "list"}, want: true},
		{args: []string{"agx", "doctor"}, want: false},
		{args: []string{"agx", "runtime", "status"}, want: false},
		{args: []string{"agx", "tui"}, want: false},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			if got := isRuntimeBackedInvocation(tt.args); got != tt.want {
				t.Fatalf("isRuntimeBackedInvocation(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestDirectInvocationRouting(t *testing.T) {
	if !isRuntimeInvocation([]string{"agx", "runtime", "status"}) {
		t.Fatal("isRuntimeInvocation(runtime status) = false, want true")
	}
	if isRuntimeInvocation([]string{"agx", "doctor"}) {
		t.Fatal("isRuntimeInvocation(doctor) = true, want false")
	}
	if !isDoctorInvocation([]string{"agx", "doctor"}) {
		t.Fatal("isDoctorInvocation(doctor) = false, want true")
	}
	if isDoctorInvocation([]string{"agx", "runtime"}) {
		t.Fatal("isDoctorInvocation(runtime) = true, want false")
	}
}

func TestVersionString(t *testing.T) {
	oldVersion, oldCommit, oldDate := version, commit, date
	t.Cleanup(func() {
		version, commit, date = oldVersion, oldCommit, oldDate
	})
	version, commit, date = "1.2.3", "abc123", "2026-01-02"
	if got, want := versionString(), "1.2.3 (abc123, 2026-01-02)"; got != want {
		t.Fatalf("versionString() = %q, want %q", got, want)
	}
}

func TestRuntimeCommandTreeAndResetValidation(t *testing.T) {
	runtimeCmd := newRuntimeCmd()
	for _, name := range []string{"start", "status", "stop", "reset", "install-service", "uninstall-service"} {
		if _, _, err := runtimeCmd.Find([]string{name}); err != nil {
			t.Fatalf("runtime command missing %q: %v", name, err)
		}
	}

	rootCmd := &cobra.Command{}
	rootCmd.AddCommand(newRuntimeClientTuiCmd(agxruntime.NewClient()))
	tuiCmd, _, err := rootCmd.Find([]string{"tui", "--once"})
	if err != nil {
		t.Fatalf("tui command missing: %v", err)
	}
	if tuiCmd.Use != "tui" {
		t.Fatalf("tui command Use = %q, want tui", tuiCmd.Use)
	}

	resetCmd := newRuntimeResetCmd()
	var out bytes.Buffer
	resetCmd.SetOut(&out)
	resetCmd.SetErr(&out)
	if err := resetCmd.Execute(); err == nil || !strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("reset without confirm error = %v, want confirmation error", err)
	}

	configDir := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)
	dbPath := filepath.Join(configDir, "agx.db")
	if err := os.WriteFile(dbPath, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	resetCmd = newRuntimeResetCmd()
	out.Reset()
	resetCmd.SetOut(&out)
	resetCmd.SetErr(&out)
	resetCmd.SetArgs([]string{"--confirm"})
	if err := resetCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Removed AGX runtime state") {
		t.Fatalf("reset output = %q, want removed state message", out.String())
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("runtime database still exists: %v", err)
	}
}

func TestDoctorCommandPrintsOfflineDiagnostics(t *testing.T) {
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	cmd := newDoctorCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"runtime: not running",
		"config dir:",
		"database:",
		"default agent:",
		"discord enabled:",
		"PATH:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, text)
		}
	}
}

func TestCompactPath(t *testing.T) {
	short := "/a:/b:/c"
	if got := compactPath(short); got != short {
		t.Fatalf("compactPath(short) = %q, want unchanged", got)
	}
	long := "/1:/2:/3:/4:/5:/6:/7:/8:/9"
	if got, want := compactPath(long), "/1:/2:/3:/4:/5:/6:/7:/8:..."; got != want {
		t.Fatalf("compactPath(long) = %q, want %q", got, want)
	}
}
