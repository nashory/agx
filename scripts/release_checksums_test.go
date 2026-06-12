package scripts

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseChecksumsWritesAllReleaseArtifacts(t *testing.T) {
	root := copyReleaseChecksumScript(t)
	writeFile(t, filepath.Join(root, "dist", "agx-linux-amd64.tar.gz"), "linux")
	writeFile(t, filepath.Join(root, "dist", "AGX-darwin-arm64.dmg"), "mac")
	writeFile(t, filepath.Join(root, "dist", "ignored.txt"), "ignored")

	runScript(t, filepath.Join(root, "scripts", "release-checksums.sh"))

	checksums, err := os.ReadFile(filepath.Join(root, "dist", "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(checksums))
	want := strings.Join([]string{
		checksumLine("mac", "dist/AGX-darwin-arm64.dmg"),
		checksumLine("linux", "dist/agx-linux-amd64.tar.gz"),
	}, "\n")
	if got != want {
		t.Fatalf("checksums.txt =\n%s\nwant:\n%s", got, want)
	}
}

func TestReleaseChecksumsFailsWithoutArtifacts(t *testing.T) {
	root := copyReleaseChecksumScript(t)
	if err := os.MkdirAll(filepath.Join(root, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(root, "scripts", "release-checksums.sh"))
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("release-checksums succeeded without artifacts; output:\n%s", output)
	}
	if !strings.Contains(string(output), "no release artifacts found") {
		t.Fatalf("output = %q, want missing artifacts message", output)
	}
}

func copyReleaseChecksumScript(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	scriptsDir := filepath.Join(root, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("release-checksums.sh")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(scriptsDir, "release-checksums.sh")
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runScript(t *testing.T, path string) {
	t.Helper()
	cmd := exec.Command(path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", path, err, output)
	}
}

func checksumLine(content, path string) string {
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x  %s", sum, path)
}
