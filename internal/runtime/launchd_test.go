package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRenderLaunchAgentPlist(t *testing.T) {
	data, err := RenderLaunchAgentPlist("/usr/local/bin/agx", "/usr/local/bin:/usr/bin:/bin")
	if err != nil {
		t.Fatalf("RenderLaunchAgentPlist() error = %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"<key>Label</key>",
		"<string>dev.agx.runtime</string>",
		"<key>ProgramArguments</key>",
		"<array>",
		"<string>/usr/local/bin/agx</string>",
		"<string>runtime</string>",
		"<string>start</string>",
		"<key>EnvironmentVariables</key>",
		"<dict>",
		"<key>PATH</key>",
		"<key>HOME</key>",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("plist missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "<Items>") {
		t.Fatalf("plist contains Go field name wrapper <Items>:\n%s", text)
	}
	if runtime.GOOS == "darwin" {
		path := filepath.Join(t.TempDir(), "dev.agx.runtime.plist")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
			t.Fatalf("plutil -lint failed: %v\n%s\n%s", err, out, text)
		}
	}
}

func TestLaunchdEnvironmentPathIncludesBinaryAndFallbacks(t *testing.T) {
	path := LaunchdEnvironmentPath("/custom/bin:/usr/bin:/custom/bin", "/opt/agx/bin/agx")
	for _, want := range []string{
		"/opt/agx/bin",
		"/custom/bin",
		"/usr/bin",
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
	} {
		if !strings.Contains(path, want) {
			t.Fatalf("LaunchdEnvironmentPath() = %q, missing %q", path, want)
		}
	}
	if strings.Count(path, "/custom/bin") != 1 {
		t.Fatalf("LaunchdEnvironmentPath() = %q, duplicated custom path", path)
	}
}
