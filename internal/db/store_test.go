package db

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpenPathCreatesPrivateDatabaseDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "agx.db")
	store, err := OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		got := info.Mode().Perm()
		t.Fatalf("database directory mode = %s, want 0700", got)
	}
}

func TestNormalizeProjectPathExpandsHomeForms(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", filepath.VolumeName(home))
	t.Setenv("HOMEPATH", strings.TrimPrefix(home, filepath.VolumeName(home)))

	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{name: "bare", in: "~", want: home},
		{name: "posix separator", in: "~/github/repo", want: filepath.Join(home, "github", "repo")},
		{name: "windows separator", in: `~\github\repo`, want: filepath.Join(home, `github\repo`)},
		{name: "trimmed", in: "  ~/github/repo  ", want: filepath.Join(home, "github", "repo")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeProjectPath(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeProjectPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
			// Check for a leading tilde only: Windows 8.3 short paths (e.g.
			// C:\Users\CRAIGS~1\...) legitimately contain '~' mid-path.
			if strings.HasPrefix(got, "~") {
				t.Fatalf("NormalizeProjectPath(%q) left tilde unexpanded: %q", tc.in, got)
			}
		})
	}
}

func TestProjectAccessGrantNormalizesHomePath(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", filepath.VolumeName(home))
	t.Setenv("HOMEPATH", strings.TrimPrefix(home, filepath.VolumeName(home)))

	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	raw := `~\github\repo`
	normalized, err := NormalizeProjectPath(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkProjectAccessGranted(raw); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{raw, normalized} {
		granted, err := store.HasProjectAccessGrant(ref)
		if err != nil {
			t.Fatal(err)
		}
		if !granted {
			t.Fatalf("HasProjectAccessGrant(%q) = false, want true", ref)
		}
	}
}

func TestProjectAndTaskCRUD(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "implement auth", nil, "claude", StatusOffline)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != StatusOffline {
		t.Fatalf("status = %s, want %s", task.Status, StatusOffline)
	}
	if task.Interface != TaskInterfaceLocal {
		t.Fatalf("interface = %s, want %s", task.Interface, TaskInterfaceLocal)
	}
	if task.WorkspaceMode != WorkspaceModeWorktree {
		t.Fatalf("workspace mode = %s, want %s", task.WorkspaceMode, WorkspaceModeWorktree)
	}
	resolved, err := store.ResolveTask(task.ID[:8])
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ID != task.ID {
		t.Fatalf("resolved task ID = %s, want %s", resolved.ID, task.ID)
	}
	session := "task-" + task.ID[:8]
	if err := store.UpdateTaskSessionAndStatus(task.ID, &session, StatusActive); err != nil {
		t.Fatal(err)
	}
	live, err := store.ListLiveTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 {
		t.Fatalf("live count = %d, want 1", len(live))
	}

	if err := store.UpdateTaskLastUserPrompt(task.ID, "  make the auth flow resilient  "); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastUserPrompt == nil || *updated.LastUserPrompt != "make the auth flow resilient" {
		t.Fatalf("LastUserPrompt = %#v, want trimmed prompt", updated.LastUserPrompt)
	}
}

func TestCreateTaskRuntimeModeInterfaceValidatesInterface(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(NewTaskID(), project.ID, "discord task", nil, "codex", true, TaskInterfaceDiscord, StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if task.Interface != TaskInterfaceDiscord {
		t.Fatalf("interface = %s, want %s", task.Interface, TaskInterfaceDiscord)
	}
	if _, err := store.CreateTaskRuntimeModeInterface(NewTaskID(), project.ID, "bad", nil, "codex", false, TaskInterface("bad"), StatusActive, nil, nil, nil); err == nil {
		t.Fatal("CreateTaskRuntimeModeInterface accepted invalid interface")
	}
}

func TestCreateTaskRuntimeModeInterfaceWorkspaceValidatesWorkspaceMode(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterfaceWorkspace(NewTaskID(), project.ID, "project task", nil, "codex", true, TaskInterfaceLocal, WorkspaceModeProject, StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if task.WorkspaceMode != WorkspaceModeProject {
		t.Fatalf("WorkspaceMode = %s, want %s", task.WorkspaceMode, WorkspaceModeProject)
	}
	if _, err := store.CreateTaskRuntimeModeInterfaceWorkspace(NewTaskID(), project.ID, "bad", nil, "codex", false, TaskInterfaceLocal, WorkspaceMode("bad"), StatusActive, nil, nil, nil); err == nil {
		t.Fatal("CreateTaskRuntimeModeInterfaceWorkspace accepted invalid workspace mode")
	}
}

func TestActiveProjectWorkspaceTaskIgnoresInactiveTasks(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTaskRuntimeModeInterfaceWorkspace(NewTaskID(), project.ID, "offline project task", nil, "codex", true, TaskInterfaceLocal, WorkspaceModeProject, StatusOffline, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActiveProjectWorkspaceTask(project.ID); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("ActiveProjectWorkspaceTask inactive error = %v, want ErrTaskNotFound", err)
	}
	active, err := store.CreateTaskRuntimeModeInterfaceWorkspace(NewTaskID(), project.ID, "active project task", nil, "codex", true, TaskInterfaceLocal, WorkspaceModeProject, StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	found, err := store.ActiveProjectWorkspaceTask(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != active.ID {
		t.Fatalf("ActiveProjectWorkspaceTask ID = %s, want %s", found.ID, active.ID)
	}
}

func TestTaskAgentStreamMetadata(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "discord task", nil, "codex", StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	threadID := "thread-1"
	cursor := "cursor-1"
	streamKind := "codex-app-server"
	if err := store.UpdateTaskAgentStream(task.ID, &threadID, &cursor, &streamKind); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.AgentThreadID == nil || *updated.AgentThreadID != threadID {
		t.Fatalf("AgentThreadID = %#v, want %q", updated.AgentThreadID, threadID)
	}
	if updated.AgentEventCursor == nil || *updated.AgentEventCursor != cursor {
		t.Fatalf("AgentEventCursor = %#v, want %q", updated.AgentEventCursor, cursor)
	}
	if updated.AgentStreamKind == nil || *updated.AgentStreamKind != streamKind {
		t.Fatalf("AgentStreamKind = %#v, want %q", updated.AgentStreamKind, streamKind)
	}
	nextCursor := "cursor-2"
	if err := store.UpdateTaskAgentEventCursor(task.ID, &nextCursor); err != nil {
		t.Fatal(err)
	}
	updated, err = store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.AgentEventCursor == nil || *updated.AgentEventCursor != nextCursor {
		t.Fatalf("AgentEventCursor = %#v, want %q", updated.AgentEventCursor, nextCursor)
	}
}

func TestUpdateTaskMetadataCanClearDescription(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	description := "original"
	task, err := store.CreateTask(project.ID, "old title", &description, "claude", StatusOffline)
	if err != nil {
		t.Fatal(err)
	}

	title := "new title"
	agent := "codex"
	if err := store.UpdateTask(task.ID, &title, nil, &agent); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != title || updated.Agent != agent || updated.Description == nil || *updated.Description != description {
		t.Fatalf("updated task = %#v, want title/agent changed and description preserved", updated)
	}

	var cleared *string
	if err := store.UpdateTask(task.ID, nil, &cleared, nil); err != nil {
		t.Fatal(err)
	}
	updated, err = store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Description != nil {
		t.Fatalf("Description = %#v, want nil after clear", updated.Description)
	}
	if updated.Title != title || updated.Agent != agent {
		t.Fatalf("updated task = %#v, want title/agent preserved after description clear", updated)
	}
}

func TestProjectUpdatesAndAccessGrant(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	path := t.TempDir()
	project, err := store.EnsureProjectDetails(path, "Original", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if granted, err := store.HasProjectAccessGrant(path); err != nil || granted {
		t.Fatalf("HasProjectAccessGrant before grant = %t, %v; want false, nil", granted, err)
	}
	if err := store.MarkProjectAccessGranted(path); err != nil {
		t.Fatal(err)
	}
	if granted, err := store.HasProjectAccessGrant(path); err != nil || !granted {
		t.Fatalf("HasProjectAccessGrant after grant = %t, %v; want true, nil", granted, err)
	}

	description := "updated description"
	if err := store.UpdateProjectDetails(project.ID, "  Renamed  ", &description); err != nil {
		t.Fatal(err)
	}
	defaultAgent := "codex"
	if err := store.UpdateProjectDefaultAgent(project.ID, &defaultAgent); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetProject(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Renamed" || updated.Description == nil || *updated.Description != description || updated.DefaultAgent == nil || *updated.DefaultAgent != defaultAgent {
		t.Fatalf("updated project = %#v, want details and default agent persisted", updated)
	}
	if err := store.UpdateProjectDetails(project.ID, " \t", nil); err == nil {
		t.Fatal("UpdateProjectDetails accepted blank name")
	}
	if err := store.UpdateProjectDefaultAgent("missing", nil); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("UpdateProjectDefaultAgent missing error = %v, want ErrProjectNotFound", err)
	}
}

func TestResolveProjectAmbiguousName(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	root := t.TempDir()
	samePath := filepath.Join(root, "same")
	otherSamePath := filepath.Join(root, "other", "same")
	if _, err := store.EnsureProject(samePath, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureProject(otherSamePath, nil); err != nil {
		t.Fatal(err)
	}
	_, err = store.ResolveProject("same")
	if !errors.Is(err, ErrProjectAmbiguous) {
		t.Fatalf("ResolveProject error = %v, want ErrProjectAmbiguous", err)
	}
	var ambiguous AmbiguousProjectError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("ResolveProject error type = %T, want AmbiguousProjectError", err)
	}
	if len(ambiguous.Matches) != 2 {
		t.Fatalf("ambiguous matches = %d, want 2", len(ambiguous.Matches))
	}
	message := err.Error()
	for _, want := range []string{samePath, otherSamePath, "Use path instead"} {
		if !strings.Contains(message, want) {
			t.Fatalf("ambiguous error missing %q:\n%s", want, message)
		}
	}
}

func TestDeleteProjectCascadesTasks(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "implement auth", nil, "claude", StatusOffline)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteProject(project.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := store.GetTask(task.ID); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("GetTask after DeleteProject error = %v, want ErrTaskNotFound", err)
	}
}

func TestResolveTaskAmbiguousID(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tasks := []struct {
		id     string
		title  string
		status TaskStatus
	}{
		{id: "aaaaaaaa-1111-1111-1111-111111111111", title: "first task", status: StatusOffline},
		{id: "aaaaaaaa-2222-2222-2222-222222222222", title: "second task", status: StatusComplete},
	}
	for _, task := range tasks {
		if _, err := store.CreateTaskWithSession(task.id, project.ID, task.title, nil, "claude", task.status, nil); err != nil {
			t.Fatal(err)
		}
	}

	_, err = store.ResolveTask("aaaaaaaa")
	if !errors.Is(err, ErrTaskAmbiguous) {
		t.Fatalf("ResolveTask error = %v, want ErrTaskAmbiguous", err)
	}
	var ambiguous AmbiguousTaskError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("ResolveTask error type = %T, want AmbiguousTaskError", err)
	}
	if len(ambiguous.Matches) != 2 {
		t.Fatalf("ambiguous matches = %d, want 2", len(ambiguous.Matches))
	}
	message := err.Error()
	for _, want := range []string{"Use a longer task id", "aaaaaaaa", "offline", "first task", "complete", "second task"} {
		if !strings.Contains(message, want) {
			t.Fatalf("ambiguous error missing %q:\n%s", want, message)
		}
	}
}

func TestResolveTaskInProjectScopesPrefix(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	firstProject, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	secondProject, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateTaskWithSession("bbbbbbbb-1111-1111-1111-111111111111", firstProject.ID, "first", nil, "claude", StatusOffline, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTaskWithSession("bbbbbbbb-2222-2222-2222-222222222222", secondProject.ID, "second", nil, "claude", StatusOffline, nil); err != nil {
		t.Fatal(err)
	}

	resolved, err := store.ResolveTaskInProject(firstProject.ID, "bbbbbbbb")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ID != first.ID {
		t.Fatalf("ResolveTaskInProject() = %s, want %s", resolved.ID, first.ID)
	}
}

func TestResolveTaskRejectsShortPrefix(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	_, err = store.ResolveTask("abc")
	if !errors.Is(err, ErrTaskIDTooShort) {
		t.Fatalf("ResolveTask error = %v, want ErrTaskIDTooShort", err)
	}
}

func TestTaskWritesRejectInvalidStatus(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTask(project.ID, "bad", nil, "claude", TaskStatus("bad")); err == nil {
		t.Fatal("CreateTask accepted invalid status")
	}
	task, err := store.CreateTask(project.ID, "good", nil, "claude", StatusOffline)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTaskStatus(task.ID, TaskStatus("bad")); err == nil {
		t.Fatal("UpdateTaskStatus accepted invalid status")
	}
	if err := store.UpdateTaskSessionAndStatus(task.ID, nil, TaskStatus("bad")); err == nil {
		t.Fatal("UpdateTaskSessionAndStatus accepted invalid status")
	}
	if err := store.UpdateTaskRuntime(task.ID, nil, TaskStatus("bad"), nil, nil); err == nil {
		t.Fatal("UpdateTaskRuntime accepted invalid status")
	}
}
