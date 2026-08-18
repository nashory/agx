//go:build !windows

package runtime

import (
	"os"
	"testing"
)

const stubCommandExt = ""

func stubCommandScript(posixScript, _ string) string { return posixScript }

// assertPromptFileIsPrivate checks the prompt file is not readable by other
// users on the host.
func assertPromptFileIsPrivate(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("prompt mode = %o, want 600", got)
	}
}
