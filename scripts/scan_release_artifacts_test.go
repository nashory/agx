package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestScanReleaseArtifactsPassesCleanTarball(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell release helper runs under Unix shells")
	}
	root := copyArtifactScanScript(t)
	dist := filepath.Join(root, "dist")
	payload := filepath.Join(root, "payload")
	writeFile(t, filepath.Join(payload, "agx"), "binary")
	makeTarball(t, filepath.Join(dist, "agx-linux-amd64.tar.gz"), payload)

	runScript(t, filepath.Join(root, "scripts", "scan-release-artifacts.sh"))
}

func TestScanReleaseArtifactsRejectsLocalState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell release helper runs under Unix shells")
	}
	root := copyArtifactScanScript(t)
	dist := filepath.Join(root, "dist")
	payload := filepath.Join(root, "payload")
	writeFile(t, filepath.Join(payload, ".agx", "config.toml"), "token = bad")
	makeTarball(t, filepath.Join(dist, "agx-linux-amd64.tar.gz"), payload)

	cmd := exec.Command(filepath.Join(root, "scripts", "scan-release-artifacts.sh"))
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("scan-release-artifacts succeeded with local state; output:\n%s", output)
	}
	if !strings.Contains(string(output), "forbidden local or secret-like paths") {
		t.Fatalf("output = %q, want forbidden path message", output)
	}
}

func copyArtifactScanScript(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	scriptsDir := filepath.Join(root, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("scan-release-artifacts.sh")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(scriptsDir, "scan-release-artifacts.sh")
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func makeTarball(t *testing.T, artifact, payload string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("tar", "-czf", artifact, "-C", payload, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tar failed: %v\n%s", err, output)
	}
}
