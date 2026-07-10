package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandHomePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory available")
	}
	cases := map[string]string{
		"~":            home,
		"~/github/agx": filepath.Join(home, "github", "agx"),
		"/abs/path":    "/abs/path",    // no ~ -> returned unchanged
		"relative/dir": "relative/dir", // no ~ -> returned unchanged
		"":             "",
	}
	for input, want := range cases {
		if got := ExpandHomePath(input); got != want {
			t.Fatalf("ExpandHomePath(%q) = %q, want %q", input, got, want)
		}
	}
}
