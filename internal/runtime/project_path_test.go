package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nashory/agx/internal/db"
)

func TestValidateProjectAccessExpandsHomePathWithWindowsSeparator(t *testing.T) {
	raw, normalized := initHomeRelativeRuntimeGitRepo(t, `~\github\kaggle-competition`)

	if err := validateOrRepairProjectAccess(raw); err != nil {
		t.Fatalf("validateOrRepairProjectAccess(%q) error = %v", raw, err)
	}
	// Check for a leading tilde only: Windows 8.3 short paths (e.g.
	// C:\Users\CRAIGS~1\...) legitimately contain '~' mid-path.
	if strings.HasPrefix(normalized, "~") {
		t.Fatalf("normalized path still has an unexpanded leading tilde: %q", normalized)
	}
}

func TestDiscordCreateProjectExpandsHomePathWithWindowsSeparator(t *testing.T) {
	raw, normalized := initHomeRelativeRuntimeGitRepo(t, `~\github\kaggle-competition`)
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	service := NewService("test-version")
	service.store = store
	summary, err := (discordCommandService{runtime: service}).CreateProject(context.Background(), raw, "Kaggle", "codex")
	if err != nil {
		t.Fatalf("CreateProject(%q) error = %v", raw, err)
	}
	if summary.Path != normalized {
		t.Fatalf("CreateProject().Path = %q, want %q", summary.Path, normalized)
	}
	project, err := store.GetProjectByPath(raw)
	if err != nil {
		t.Fatalf("GetProjectByPath(%q) error = %v", raw, err)
	}
	if project.Path != normalized {
		t.Fatalf("stored project path = %q, want %q", project.Path, normalized)
	}
	granted, err := store.HasProjectAccessGrant(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !granted {
		t.Fatalf("HasProjectAccessGrant(%q) = false, want true", raw)
	}
}

func initHomeRelativeRuntimeGitRepo(t *testing.T, raw string) (string, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", filepath.VolumeName(home))
	t.Setenv("HOMEPATH", strings.TrimPrefix(home, filepath.VolumeName(home)))

	normalized, err := db.NormalizeProjectPath(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(normalized, 0o700); err != nil {
		t.Fatal(err)
	}
	runRuntimeTestCommand(t, normalized, "git", "init", "-q")
	runRuntimeTestCommand(t, normalized, "git", "config", "user.email", "agx@example.com")
	runRuntimeTestCommand(t, normalized, "git", "config", "user.name", "AGX Test")
	if err := os.WriteFile(filepath.Join(normalized, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runRuntimeTestCommand(t, normalized, "git", "add", "README.md")
	runRuntimeTestCommand(t, normalized, "git", "commit", "-q", "-m", "initial")
	return raw, normalized
}
