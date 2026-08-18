package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// batchLines joins batch script lines with CRLF, which cmd.exe relies on to
// parse parenthesised blocks reliably.
func batchLines(lines ...string) string {
	return strings.Join(lines, "\r\n") + "\r\n"
}

// Stub agent CLIs for the exec-backed tests. Windows needs its own script
// flavour and a PATHEXT suffix: exec.LookPath only resolves PATHEXT extensions
// there, so an extensionless stub is skipped and a real agent binary further
// along PATH gets launched instead.
const stubExitZeroPosix = "#!/bin/sh\nexit 0\n"

var stubExitZeroBatch = batchLines("@echo off", "exit /b 0")

// writeStubCommand installs a fake executable named name in dir and returns its
// path, picking the script flavour and file extension for the host platform.
func writeStubCommand(t *testing.T, dir, name, posixScript, batchScript string) string {
	t.Helper()
	path := filepath.Join(dir, name+stubCommandExt)
	if err := os.WriteFile(path, []byte(stubCommandScript(posixScript, batchScript)), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeStubCommandOnPath installs the stub in a fresh directory placed first on
// PATH and returns that directory.
//
// It then asserts the stub is what exec.LookPath resolves. Without that check a
// stub the host cannot execute is skipped silently and a real agent CLI further
// along PATH gets launched instead, which turns a unit test into a live agent
// session that blocks until the test binary times out.
func writeStubCommandOnPath(t *testing.T, name, posixScript, batchScript string) string {
	t.Helper()
	dir := t.TempDir()
	path := writeStubCommand(t, dir, name, posixScript, batchScript)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	assertStubResolves(t, name, path)
	return dir
}

func assertStubResolves(t *testing.T, name, path string) {
	t.Helper()
	resolved, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("stub %q does not resolve on PATH: %v", name, err)
	}
	want, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.Stat(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(want, got) {
		t.Fatalf("%q resolved to %q, want the stub at %q", name, resolved, path)
	}
}
