package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResetStateRemovesRuntimeFilesAndPreservesConfig(t *testing.T) {
	configDir := shortTempDir(t)
	t.Setenv("AGX_CONFIG_DIR", configDir)
	writeFile(t, filepath.Join(configDir, "agx.db"))
	writeFile(t, filepath.Join(configDir, "agx.db-shm"))
	writeFile(t, filepath.Join(configDir, "agx.db-wal"))
	writeFile(t, filepath.Join(configDir, "config.toml"))
	if err := os.MkdirAll(filepath.Join(configDir, "streams"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(configDir, "worktrees"), 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := ResetState(context.Background(), ResetOptions{})
	if err != nil {
		t.Fatalf("ResetState() error = %v", err)
	}
	if len(result.Removed) < 5 {
		t.Fatalf("ResetState().Removed = %#v, want runtime files removed", result.Removed)
	}
	assertMissing(t, filepath.Join(configDir, "agx.db"))
	assertMissing(t, filepath.Join(configDir, "streams"))
	assertMissing(t, filepath.Join(configDir, "worktrees"))
	if _, err := os.Stat(filepath.Join(configDir, "config.toml")); err != nil {
		t.Fatalf("config.toml was not preserved: %v", err)
	}
}

func TestResetStateCanRemoveConfig(t *testing.T) {
	configDir := shortTempDir(t)
	t.Setenv("AGX_CONFIG_DIR", configDir)
	writeFile(t, filepath.Join(configDir, "config.toml"))
	if _, err := ResetState(context.Background(), ResetOptions{IncludeConfig: true}); err != nil {
		t.Fatalf("ResetState() error = %v", err)
	}
	assertMissing(t, filepath.Join(configDir, "config.toml"))
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s still exists or stat failed differently: %v", path, err)
	}
}
