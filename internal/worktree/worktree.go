package worktree

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/nashory/agx/internal/config"
	"github.com/nashory/agx/internal/db"
	"github.com/nashory/agx/internal/display"
)

// gitWorktreeMu serializes git-worktree mutations for a repository. Git stores
// shared metadata under the source repository, so concurrent add/remove calls
// can otherwise race even when they operate on different task worktrees.
var gitWorktreeMu sync.Mutex

// Prepared describes the filesystem workspace selected for a task.
//
// When Created is false and Path is nil, the task runs directly in the project
// checkout. When Created is true, Path and Branch identify the per-task git
// worktree and branch that must be cleaned up when the task is removed.
type Prepared struct {
	WorkingDir string
	Path       *string
	Branch     *string
	Base       *string
	Created    bool
}

// ValidateProject verifies that AGX can create and remove git worktrees for the
// project. It intentionally uses a real detached worktree because filesystem
// permission prompts and git configuration problems often only appear at
// worktree-add time.
func ValidateProject(projectPath string) error {
	if err := ensureWorktreeBaseCommit(projectPath); err != nil {
		return err
	}
	parent := filepath.Join(config.ConfigDir(), "worktrees", ".validation")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create validation worktree parent: %w", err)
	}
	target, err := os.MkdirTemp(parent, "worktree-add-*")
	if err != nil {
		return fmt.Errorf("create validation worktree path: %w", err)
	}
	if err := os.Remove(target); err != nil {
		return fmt.Errorf("prepare validation worktree path: %w", err)
	}
	gitWorktreeMu.Lock()
	defer gitWorktreeMu.Unlock()
	defer func() { _ = os.RemoveAll(target) }()
	if err := runGit(projectPath, "worktree", "add", "--detach", "--", target, "HEAD"); err != nil {
		return fmt.Errorf("git worktree add validation failed: %w", err)
	}
	defer func() {
		_ = runGit(projectPath, "worktree", "remove", "--force", "--", target)
	}()
	if err := ensureWritableDirectory(target); err != nil {
		return err
	}
	return nil
}

// Prepare creates a fresh task workspace according to cfg, or returns the
// project path directly when worktrees are disabled.
func Prepare(project db.Project, taskID string, cfg config.WorktreeConfig) (Prepared, error) {
	return PrepareForTask(project, taskID, cfg, nil, nil)
}

// PrepareForTask resolves the workspace for a task, preferring an existing
// stored worktree when possible. This is used during task restart and recovery
// so AGX can resume into the same branch instead of accidentally creating a
// second workspace for the same task.
func PrepareForTask(project db.Project, taskID string, cfg config.WorktreeConfig, existingPath, existingBranch *string) (Prepared, error) {
	if existingPath != nil && *existingPath != "" {
		cleanPath := filepath.Clean(*existingPath)
		if info, err := os.Stat(cleanPath); err == nil && info.IsDir() {
			if err := ensureWritableDirectory(cleanPath); err != nil {
				return Prepared{}, err
			}
			return Prepared{WorkingDir: cleanPath, Path: &cleanPath, Branch: existingBranch}, nil
		}
		if existingBranch != nil && *existingBranch != "" {
			if branchExists(project.Path, *existingBranch) {
				if err := os.MkdirAll(filepath.Dir(cleanPath), 0o755); err != nil {
					return Prepared{}, fmt.Errorf("create worktree parent: %w", err)
				}
				gitWorktreeMu.Lock()
				defer gitWorktreeMu.Unlock()
				_ = runGit(project.Path, "worktree", "prune")
				if err := runGit(project.Path, "worktree", "add", "--", cleanPath, *existingBranch); err != nil {
					return Prepared{}, err
				}
				if err := ensureWritableDirectory(cleanPath); err != nil {
					return Prepared{}, err
				}
				return Prepared{WorkingDir: cleanPath, Path: &cleanPath, Branch: existingBranch, Created: true}, nil
			}
		}
	}
	if !cfg.Enabled {
		if err := ensureWritableDirectory(project.Path); err != nil {
			return Prepared{}, err
		}
		return Prepared{WorkingDir: project.Path}, nil
	}
	if err := ensureWorktreeBaseCommit(project.Path); err != nil {
		return Prepared{}, err
	}
	shortID := display.ShortID(taskID)
	worktreeRoot, err := projectWorktreeRoot(project)
	if err != nil {
		return Prepared{}, err
	}
	worktreePath := filepath.Join(worktreeRoot, "task-"+shortID)
	branchName := "agx/task-" + shortID
	baseBranch := cfg.BaseBranch
	if baseBranch == "" {
		current, err := currentBranch(project.Path)
		if err != nil {
			return Prepared{}, err
		}
		baseBranch = current
	}
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return Prepared{}, fmt.Errorf("create worktree parent: %w", err)
	}
	gitWorktreeMu.Lock()
	defer gitWorktreeMu.Unlock()
	_ = runGit(project.Path, "worktree", "prune")
	if err := runGit(project.Path, "worktree", "add", "-b", branchName, "--", worktreePath, baseBranch); err != nil {
		return Prepared{}, err
	}
	cleanPath := filepath.Clean(worktreePath)
	if err := ensureWritableDirectory(cleanPath); err != nil {
		return Prepared{}, err
	}
	return Prepared{WorkingDir: cleanPath, Path: &cleanPath, Branch: &branchName, Base: &baseBranch, Created: true}, nil
}

func branchExists(projectPath, branchName string) bool {
	branchName = strings.TrimSpace(branchName)
	if branchName == "" {
		return false
	}
	return runGit(projectPath, "rev-parse", "--verify", "--quiet", "refs/heads/"+branchName) == nil
}

// Remove deletes a task worktree and branch only when it is safe to do so. It
// refuses dirty worktrees and branches that contain commits not merged into the
// recorded base branch.
func Remove(project db.Project, worktreePath, branchName, baseBranch *string) error {
	if err := ensureRemovable(project, worktreePath, branchName, baseBranch); err != nil {
		return err
	}
	return remove(project, worktreePath, branchName, false)
}

// RemoveForce tears down generated task workspace state without preserving user
// changes. Use it for recovery/reset paths where the caller has already decided
// the runtime state is stale.
func RemoveForce(project db.Project, worktreePath, branchName *string) error {
	return remove(project, worktreePath, branchName, true)
}

// CleanupOrphans removes AGX task worktree directories that are no longer
// associated with active tasks. activePaths must contain cleaned absolute paths
// for every live task worktree that should be preserved.
func CleanupOrphans(project db.Project, activePaths map[string]bool) (int, error) {
	root, err := projectWorktreeRoot(project)
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "task-") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		cleanPath := filepath.Clean(path)
		if activePaths[cleanPath] {
			continue
		}
		branch := "agx/" + entry.Name()
		if err := RemoveForce(project, &cleanPath, &branch); err != nil {
			errs = append(errs, fmt.Errorf("cleanup orphan %s: %w", cleanPath, err))
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}

func remove(project db.Project, worktreePath, branchName *string, force bool) error {
	gitWorktreeMu.Lock()
	defer gitWorktreeMu.Unlock()
	var firstErr error
	if worktreePath != nil && *worktreePath != "" {
		if err := validateWorktreePath(project, *worktreePath); err != nil {
			return err
		}
		if force {
			_ = makeTreeWritable(*worktreePath)
		}
		args := []string{"worktree", "remove"}
		if force {
			args = append(args, "--force")
		}
		args = append(args, "--", *worktreePath)
		if err := runGit(project.Path, args...); err != nil {
			firstErr = err
			if force {
				if cleanupErr := removeFilesystemTree(*worktreePath); cleanupErr == nil {
					firstErr = nil
				} else {
					firstErr = errors.Join(firstErr, cleanupErr)
				}
			}
		}
	}
	if branchName != nil && *branchName != "" {
		deleteFlag := "-d"
		if force {
			deleteFlag = "-D"
		}
		if err := runGit(project.Path, "branch", deleteFlag, "--", *branchName); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func removeFilesystemTree(path string) error {
	if err := makeTreeWritable(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.RemoveAll(path)
}

func makeTreeWritable(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		mode := info.Mode().Perm()
		if entry.IsDir() {
			mode |= 0o700
		} else {
			mode |= 0o600
		}
		if info.Mode().Perm() == mode {
			return nil
		}
		return os.Chmod(path, mode)
	})
}

func ensureRemovable(project db.Project, worktreePath, branchName, baseBranch *string) error {
	if worktreePath != nil && *worktreePath != "" {
		if err := validateWorktreePath(project, *worktreePath); err != nil {
			return err
		}
		out, err := gitOutput(*worktreePath, "status", "--porcelain")
		if err != nil {
			return err
		}
		if strings.TrimSpace(out) != "" {
			return fmt.Errorf("worktree has uncommitted changes: %s", *worktreePath)
		}
	}
	if branchName != nil && *branchName != "" {
		target := "HEAD"
		if baseBranch != nil && *baseBranch != "" {
			target = *baseBranch
		}
		if err := runGit(project.Path, "merge-base", "--is-ancestor", "--", *branchName, target); err != nil {
			return fmt.Errorf("branch %s is not merged into %s", *branchName, target)
		}
	}
	return nil
}

func validateWorktreePath(project db.Project, path string) error {
	worktreeRoot, err := projectWorktreeRoot(project)
	if err != nil {
		return err
	}
	cleanPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return err
	}
	if cleanPath != worktreeRoot && !strings.HasPrefix(cleanPath, worktreeRoot+string(os.PathSeparator)) {
		return fmt.Errorf("worktree path escapes project worktree directory: %s", path)
	}
	return nil
}

func projectWorktreeRoot(project db.Project) (string, error) {
	base := project.ID
	if base == "" {
		base = project.Path
	}
	return filepath.Join(config.ConfigDir(), "worktrees", pathID(base)), nil
}

var unsafePathIDPattern = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func pathID(value string) string {
	clean := strings.Trim(unsafePathIDPattern.ReplaceAllString(value, "-"), "-._")
	if clean == "" {
		clean = "project"
	}
	sum := sha1.Sum([]byte(value))
	suffix := hex.EncodeToString(sum[:])[:10]
	if len(clean) > 48 {
		clean = clean[:48]
	}
	return clean + "-" + suffix
}

func HasConflicts(worktreePath *string) (bool, error) {
	if worktreePath == nil || *worktreePath == "" {
		return false, nil
	}
	out, err := gitOutput(*worktreePath, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func currentBranch(projectPath string) (string, error) {
	out, err := gitOutput(projectPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(out)
	if branch == "" || branch == "HEAD" {
		return "HEAD", nil
	}
	return branch, nil
}

func ensureWorktreeBaseCommit(projectPath string) error {
	if _, err := gitOutput(projectPath, "rev-parse", "--git-dir"); err != nil {
		return err
	}
	if _, err := gitOutput(projectPath, "rev-parse", "--verify", "HEAD^{commit}"); err != nil {
		return fmt.Errorf("git repository has no commits; create an initial commit before granting access or creating worktree tasks")
	}
	return nil
}

func runGit(dir string, args ...string) error {
	_, err := gitOutput(dir, args...)
	return err
}

func ensureWritableDirectory(path string) error {
	script := `test_path="$1/.agx-write-test-$$"; : > "$test_path" && rm -f "$test_path"`
	output, err := exec.Command("/bin/sh", "-c", script, "agx-write-test", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("working directory is not writable by AGX child processes: %s: %s: %w", path, strings.TrimSpace(string(output)), err)
	}
	return nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return string(out), nil
}
