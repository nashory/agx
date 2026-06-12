package runtime

import (
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
		"<string>/usr/local/bin/agx</string>",
		"<string>runtime</string>",
		"<string>start</string>",
		"<key>EnvironmentVariables</key>",
		"<key>PATH</key>",
		"<key>HOME</key>",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("plist missing %q:\n%s", want, text)
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
