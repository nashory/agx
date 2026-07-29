package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscordCommandServiceRuntimeLogsReadsRuntimeLogFiles(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)
	logDir := filepath.Join(configDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stdoutPath, stderrPath := RuntimeLogPaths()
	if err := os.WriteFile(stderrPath, []byte("err-1\nerr-2\nerr-3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stdoutPath, []byte("out-1\nout-2\nout-3\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	logs, err := (discordCommandService{}).RuntimeLogs(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs, "runtime.err.log") || !strings.Contains(logs, "runtime.log") {
		t.Fatalf("RuntimeLogs() = %q, want both log sections", logs)
	}
	if strings.Contains(logs, "err-1") || strings.Contains(logs, "out-1") {
		t.Fatalf("RuntimeLogs() = %q, want only the last two lines per file", logs)
	}
	if !strings.Contains(logs, "err-2\nerr-3") || !strings.Contains(logs, "out-2\nout-3") {
		t.Fatalf("RuntimeLogs() = %q, want tailed stderr and stdout logs", logs)
	}
	if strings.Index(logs, "runtime.err.log") > strings.Index(logs, "runtime.log") {
		t.Fatalf("RuntimeLogs() = %q, want stderr before stdout", logs)
	}
}

func TestDiscordCommandServiceRuntimeLogsHandlesMissingFiles(t *testing.T) {
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())

	logs, err := (discordCommandService{}).RuntimeLogs(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs, "not found") {
		t.Fatalf("RuntimeLogs() = %q, want missing file diagnostics", logs)
	}
}
