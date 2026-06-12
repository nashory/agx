//go:build linux

package runtime

import (
	"strings"
	"testing"
)

func TestRenderSystemdUserUnit(t *testing.T) {
	unit, err := RenderSystemdUserUnit(
		`/opt/agx/bin/agx`,
		`/opt/agx/bin:/usr/local/bin:/usr/bin`,
		`/home/agx`,
		`/home/agx/.config/agx`,
	)
	if err != nil {
		t.Fatalf("RenderSystemdUserUnit() error = %v", err)
	}
	text := string(unit)
	for _, want := range []string{
		"[Unit]",
		"Description=AGX Runtime",
		"[Service]",
		"Type=simple",
		`ExecStart="/opt/agx/bin/agx" runtime start`,
		"Restart=always",
		"RestartSec=2",
		`Environment="PATH=/opt/agx/bin:/usr/local/bin:/usr/bin"`,
		`Environment="HOME=/home/agx"`,
		`Environment="AGX_CONFIG_DIR=/home/agx/.config/agx"`,
		"[Install]",
		"WantedBy=default.target",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("systemd unit missing %q:\n%s", want, text)
		}
	}
}

func TestRenderSystemdUserUnitRejectsEmptyExecutable(t *testing.T) {
	if _, err := RenderSystemdUserUnit("  ", "", "", ""); err == nil {
		t.Fatal("RenderSystemdUserUnit(empty executable) error = nil, want error")
	}
}

func TestSystemdUserUnitPathPrefersHomeEnvironment(t *testing.T) {
	t.Setenv("HOME", "/home/agx")
	got, err := SystemdUserUnitPath()
	if err != nil {
		t.Fatal(err)
	}
	want := "/home/agx/.config/systemd/user/dev.agx.runtime.service"
	if got != want {
		t.Fatalf("SystemdUserUnitPath() = %q, want %q", got, want)
	}
}

func TestSystemdQuoteEscapesUnsafeCharacters(t *testing.T) {
	got := systemdQuote("A\\B\"C\nD")
	want := `"A\\B\"C\nD"`
	if got != want {
		t.Fatalf("systemdQuote() = %q, want %q", got, want)
	}
}
