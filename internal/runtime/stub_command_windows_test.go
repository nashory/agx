//go:build windows

package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const stubCommandExt = ".cmd"

func stubCommandScript(_, batchScript string) string { return batchScript }

// assertPromptFileIsPrivate cannot inspect permission bits on Windows: Go maps
// only the read-only attribute into FileMode, so a file created with 0600 always
// stats as 0666. Privacy comes from the per-user ACL on the temp directory
// instead, so check the prompt landed there.
func assertPromptFileIsPrivate(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	tempDir, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resolved, tempDir+string(os.PathSeparator)) {
		t.Fatalf("prompt path = %q, want a file under the per-user temp dir %q", resolved, tempDir)
	}
}
