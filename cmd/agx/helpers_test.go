package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/nashory/agx/internal/db"
)

func TestCLIHelpers(t *testing.T) {
	if got := maskedToken(""); got != "(not set)" {
		t.Fatalf("maskedToken(empty) = %q", got)
	}
	if got := maskedToken("abc"); got != "abc..." {
		t.Fatalf("maskedToken(short) = %q", got)
	}
	if got := maskedToken("abcd-secret"); got != "abcd..." {
		t.Fatalf("maskedToken(long) = %q", got)
	}
	if got := emptyPlaceholder(""); got != "(not set)" {
		t.Fatalf("emptyPlaceholder(empty) = %q", got)
	}
	if got := emptyPlaceholder("guild"); got != "guild" {
		t.Fatalf("emptyPlaceholder(value) = %q", got)
	}
	if got := firstConfiguredUser([]string{"user-1", "user-2"}); got != "user-1" {
		t.Fatalf("firstConfiguredUser() = %q", got)
	}
	if got := firstConfiguredUser(nil); got != "" {
		t.Fatalf("firstConfiguredUser(nil) = %q", got)
	}
}

func TestExitCodeFor(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{err: nil, want: 0},
		{err: db.ErrTaskNotFound, want: 3},
		{err: db.ErrProjectNotFound, want: 4},
		{err: errors.New("tmux failed"), want: 5},
		{err: errors.New("agent missing"), want: 2},
		{err: errors.New("other"), want: 1},
	}
	for _, tt := range tests {
		if got := exitCodeFor(tt.err); got != tt.want {
			t.Fatalf("exitCodeFor(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
}

func TestFindGitRoot(t *testing.T) {
	root := t.TempDir()
	runCLIHelperCommand(t, root, "git", "init", "-q")
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := findGitRoot(nested)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err = filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("findGitRoot() = %q, want %q", got, want)
	}
}

func runCLIHelperCommand(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}
