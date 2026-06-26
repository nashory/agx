package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nashory/agx/internal/config"
	"github.com/nashory/agx/internal/db"
)

func TestPrepareAndRemoveWorktree(t *testing.T) {
	root := initGitRepo(t)
	project := db.Project{ID: "project", Name: "repo", Path: root}

	prepared, err := Prepare(project, "12345678-aaaa", config.WorktreeConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Path == nil || prepared.Branch == nil {
		t.Fatalf("prepared worktree missing path or branch: %#v", prepared)
	}
	if prepared.WorkingDir != filepath.Clean(*prepared.Path) {
		t.Fatalf("WorkingDir = %q, want %q", prepared.WorkingDir, filepath.Clean(*prepared.Path))
	}
	if _, err := os.Stat(prepared.WorkingDir); err != nil {
		t.Fatal(err)
	}

	if err := Remove(project, prepared.Path, prepared.Branch, prepared.Base); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(prepared.WorkingDir); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists after remove: %v", err)
	}
}

func TestPrepareForTaskReusesExistingWorktree(t *testing.T) {
	root := initGitRepo(t)
	project := db.Project{ID: "project", Name: "repo", Path: root}
	prepared, err := Prepare(project, "12345678-aaaa", config.WorktreeConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = Remove(project, prepared.Path, prepared.Branch, prepared.Base)
	}()

	reused, err := PrepareForTask(project, "12345678-aaaa", config.WorktreeConfig{Enabled: true}, prepared.Path, prepared.Branch)
	if err != nil {
		t.Fatal(err)
	}
	if reused.WorkingDir != prepared.WorkingDir {
		t.Fatalf("WorkingDir = %q, want %q", reused.WorkingDir, prepared.WorkingDir)
	}
}

func TestPrepareForTaskRecreatesMissingWorktreeWhenStoredBranchIsGone(t *testing.T) {
	root := initGitRepo(t)
	project := db.Project{ID: "project", Name: "repo", Path: root}
	taskID := "12345678-aaaa"
	prepared, err := Prepare(project, taskID, config.WorktreeConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	missingPath := *prepared.Path
	missingBranch := *prepared.Branch
	if err := RemoveForce(project, prepared.Path, prepared.Branch); err != nil {
		t.Fatal(err)
	}

	recreated, err := PrepareForTask(project, taskID, config.WorktreeConfig{Enabled: true}, &missingPath, &missingBranch)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = RemoveForce(project, recreated.Path, recreated.Branch)
	}()
	if recreated.Path == nil || *recreated.Path != filepath.Clean(missingPath) {
		t.Fatalf("recreated path = %#v, want %q", recreated.Path, filepath.Clean(missingPath))
	}
	if recreated.Branch == nil || *recreated.Branch != missingBranch {
		t.Fatalf("recreated branch = %#v, want %q", recreated.Branch, missingBranch)
	}
	if _, err := os.Stat(recreated.WorkingDir); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveRefusesDirtyWorktree(t *testing.T) {
	root := initGitRepo(t)
	project := db.Project{ID: "project", Name: "repo", Path: root}
	prepared, err := Prepare(project, "12345678-aaaa", config.WorktreeConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(filepath.Join(prepared.WorkingDir, "dirty.txt"))
		_ = Remove(project, prepared.Path, prepared.Branch, prepared.Base)
	}()
	if err := os.WriteFile(filepath.Join(prepared.WorkingDir, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Remove(project, prepared.Path, prepared.Branch, prepared.Base); err == nil {
		t.Fatal("Remove succeeded with dirty worktree")
	}
	if _, err := os.Stat(prepared.WorkingDir); err != nil {
		t.Fatalf("dirty worktree was removed: %v", err)
	}
}

func TestRemoveForceRemovesReadOnlyUntrackedCache(t *testing.T) {
	root := initGitRepo(t)
	project := db.Project{ID: "project", Name: "repo", Path: root}
	prepared, err := Prepare(project, "12345678-aaaa", config.WorktreeConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(prepared.WorkingDir, ".gopath", "pkg", "mod", "example.com", "module@v1.0.0")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cacheFile := filepath.Join(cacheDir, "module.go")
	if err := os.WriteFile(cacheFile, []byte("package module\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cacheDir, 0o555); err != nil {
		t.Fatal(err)
	}

	if err := RemoveForce(project, prepared.Path, prepared.Branch); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(prepared.WorkingDir); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists after force remove: %v", err)
	}
}

func TestRemoveForceIgnoresAlreadyDeletedBranch(t *testing.T) {
	root := initGitRepo(t)
	project := db.Project{ID: "project", Name: "repo", Path: root}
	prepared, err := Prepare(project, "12345678-aaaa", config.WorktreeConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveForce(project, prepared.Path, prepared.Branch); err != nil {
		t.Fatal(err)
	}

	if err := RemoveForce(project, prepared.Path, prepared.Branch); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(prepared.WorkingDir); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists after force remove: %v", err)
	}
}

func TestRemoveRefusesUnmergedBranch(t *testing.T) {
	root := initGitRepo(t)
	project := db.Project{ID: "project", Name: "repo", Path: root}
	prepared, err := Prepare(project, "12345678-aaaa", config.WorktreeConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		run(t, root, "git", "merge", "--no-ff", "-m", "merge task", *prepared.Branch)
		_ = Remove(project, prepared.Path, prepared.Branch, prepared.Base)
	}()
	if err := os.WriteFile(filepath.Join(prepared.WorkingDir, "task.txt"), []byte("task\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, prepared.WorkingDir, "git", "add", "task.txt")
	run(t, prepared.WorkingDir, "git", "commit", "-q", "-m", "task change")

	if err := Remove(project, prepared.Path, prepared.Branch, prepared.Base); err == nil {
		t.Fatal("Remove succeeded with unmerged branch")
	}
	if _, err := os.Stat(prepared.WorkingDir); err != nil {
		t.Fatalf("unmerged worktree was removed: %v", err)
	}
}

func TestPrepareDisabledUsesProjectPath(t *testing.T) {
	root := t.TempDir()
	project := db.Project{ID: "project", Name: "repo", Path: root}
	prepared, err := Prepare(project, "12345678-aaaa", config.WorktreeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.WorkingDir != project.Path || prepared.Path != nil || prepared.Branch != nil {
		t.Fatalf("Prepare disabled = %#v", prepared)
	}
}

func TestPrepareDisabledRejectsReadOnlyProjectPath(t *testing.T) {
	root := t.TempDir()
	project := db.Project{ID: "project", Name: "repo", Path: root}
	makeReadOnly(t, root)

	if _, err := Prepare(project, "12345678-aaaa", config.WorktreeConfig{}); err == nil {
		t.Fatal("Prepare succeeded for read-only project path")
	}
}

func TestValidateProjectAddsTemporaryWorktree(t *testing.T) {
	root := initGitRepo(t)
	if err := ValidateProject(root); err != nil {
		t.Fatal(err)
	}
}

func TestValidateProjectRejectsNonGitDirectory(t *testing.T) {
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	if err := ValidateProject(t.TempDir()); err == nil {
		t.Fatal("ValidateProject succeeded for non-git directory")
	}
}

func TestValidateProjectRejectsRepositoryWithoutCommits(t *testing.T) {
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	run(t, root, "git", "init", "-q")

	err := ValidateProject(root)
	if err == nil || !strings.Contains(err.Error(), "git repository has no commits") {
		t.Fatalf("ValidateProject error = %v, want no commits guidance", err)
	}
}

func TestPrepareWorktreeRejectsRepositoryWithoutCommits(t *testing.T) {
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	run(t, root, "git", "init", "-q")
	project := db.Project{ID: "project", Name: "repo", Path: root}

	_, err := Prepare(project, "12345678-aaaa", config.WorktreeConfig{Enabled: true})
	if err == nil || !strings.Contains(err.Error(), "git repository has no commits") {
		t.Fatalf("Prepare error = %v, want no commits guidance", err)
	}
}

func TestCleanupOrphansRemovesInactiveTaskWorktrees(t *testing.T) {
	root := initGitRepo(t)
	project := db.Project{ID: "project", Name: "repo", Path: root}
	active, err := Prepare(project, "11111111-aaaa", config.WorktreeConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := Prepare(project, "22222222-bbbb", config.WorktreeConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = RemoveForce(project, active.Path, active.Branch)
	}()

	removed, err := CleanupOrphans(project, map[string]bool{filepath.Clean(*active.Path): true})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(active.WorkingDir); err != nil {
		t.Fatalf("active worktree was removed: %v", err)
	}
	if _, err := os.Stat(orphan.WorkingDir); !os.IsNotExist(err) {
		t.Fatalf("orphan worktree still exists: %v", err)
	}
	if err := exec.Command("git", "-C", root, "rev-parse", "--verify", *orphan.Branch).Run(); err == nil {
		t.Fatalf("orphan branch %s still exists", *orphan.Branch)
	}
}

func TestCleanupOrphansNoWorktreeRoot(t *testing.T) {
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	project := db.Project{ID: "project", Name: "repo", Path: t.TempDir()}
	removed, err := CleanupOrphans(project, nil)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
}

func TestCleanupOrphansIgnoresNonTaskDirectories(t *testing.T) {
	root := initGitRepo(t)
	project := db.Project{ID: "project", Name: "repo", Path: root}
	worktreeRoot, err := projectWorktreeRoot(project)
	if err != nil {
		t.Fatal(err)
	}
	notesDir := filepath.Join(worktreeRoot, "notes")
	if err := os.MkdirAll(notesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	removed, err := CleanupOrphans(project, nil)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0 for non-task directory", removed)
	}
	if _, err := os.Stat(notesDir); err != nil {
		t.Fatalf("non-task directory was removed: %v", err)
	}
}

func TestHasConflictsDetectsUnmergedFiles(t *testing.T) {
	root := initGitRepo(t)
	project := db.Project{ID: "project", Name: "repo", Path: root}
	prepared, err := Prepare(project, "12345678-aaaa", config.WorktreeConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = exec.Command("git", "-C", prepared.WorkingDir, "merge", "--abort").Run()
		_ = Remove(project, prepared.Path, prepared.Branch, prepared.Base)
	}()

	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, root, "git", "add", "README.md")
	run(t, root, "git", "commit", "-q", "-m", "main change")

	if err := os.WriteFile(filepath.Join(prepared.WorkingDir, "README.md"), []byte("task\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, prepared.WorkingDir, "git", "add", "README.md")
	run(t, prepared.WorkingDir, "git", "commit", "-q", "-m", "task change")
	if err := exec.Command("git", "-C", prepared.WorkingDir, "merge", "master").Run(); err == nil {
		t.Fatal("merge unexpectedly succeeded")
	}

	hasConflicts, err := HasConflicts(prepared.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !hasConflicts {
		t.Fatal("HasConflicts() = false, want true")
	}
}

func makeReadOnly(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(path, 0o755)
	})
	if file, err := os.CreateTemp(path, ".agx-write-test-*"); err == nil {
		name := file.Name()
		_ = file.Close()
		_ = os.Remove(name)
		t.Skip("read-only directory is writable on this host")
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	run(t, root, "git", "init", "-q")
	run(t, root, "git", "config", "user.email", "agx@example.com")
	run(t, root, "git", "config", "user.name", "AGX Test")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, root, "git", "add", "README.md")
	run(t, root, "git", "commit", "-q", "-m", "initial")
	return root
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}
