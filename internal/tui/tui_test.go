package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
	agxruntime "github.com/nashory/agx/internal/runtime"
)

type fakeClient struct {
	status   agxruntime.Status
	projects []agxruntime.Project
	active   []agxruntime.MonitorTask
	recent   []agxruntime.Task
	discord  agxdiscord.Status
	err      error
}

func (c fakeClient) Status(context.Context) (agxruntime.Status, error) {
	if c.err != nil {
		return agxruntime.Status{}, c.err
	}
	return c.status, nil
}

func (c fakeClient) ListProjects(context.Context) ([]agxruntime.Project, error) {
	return c.projects, nil
}

func (c fakeClient) MonitorTasks(context.Context) ([]agxruntime.MonitorTask, error) {
	return c.active, nil
}

func (c fakeClient) ListTasks(context.Context, string) ([]agxruntime.Task, error) {
	return c.recent, nil
}

func (c fakeClient) DiscordStatus(context.Context) (agxdiscord.Status, error) {
	return c.discord, nil
}

func TestFetchSnapshotAndRender(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	snapshot := FetchSnapshot(context.Background(), fakeClient{
		status: agxruntime.Status{PID: 42, UptimeSeconds: 7, SocketPath: "/tmp/agx.sock"},
		projects: []agxruntime.Project{
			{ID: "project-1", Name: "agx"},
		},
		active: []agxruntime.MonitorTask{
			{
				Task:        agxruntime.Task{ID: "task-123456", Title: "Implement Linux TUI", Agent: "codex", Status: db.StatusActive},
				ProjectName: "agx",
			},
		},
		recent: []agxruntime.Task{
			{ID: "task-123456", Title: "Implement Linux TUI", Agent: "codex", Status: db.StatusActive, CreatedAt: now},
		},
		discord: agxdiscord.Status{Enabled: true, Connected: true, GuildName: "AGX"},
	})
	snapshot.RefreshedAt = now
	text := RenderSnapshot(snapshot, 100)
	for _, want := range []string{
		"runtime: ok pid=42 uptime=7s",
		"projects: 1",
		"discord: connected to AGX",
		"Active Tasks",
		"Implement Linux TUI",
		"keys: r refresh, q quit",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("rendered snapshot missing %q:\n%s", want, text)
		}
	}
}

func TestRenderOfflineSnapshot(t *testing.T) {
	snapshot := FetchSnapshot(context.Background(), fakeClient{err: errors.New("socket missing")})
	text := RenderSnapshot(snapshot, 80)
	if !strings.Contains(text, "runtime: offline (socket missing)") {
		t.Fatalf("offline render missing runtime error:\n%s", text)
	}
	if !strings.Contains(text, "agx runtime start") {
		t.Fatalf("offline render missing start hint:\n%s", text)
	}
}

func TestInitialModelDoesNotRenderEmptyRuntimeAsHealthy(t *testing.T) {
	view := newModel(context.Background(), fakeClient{}, time.Second).View()
	if strings.Contains(view, "runtime: ok pid=0") {
		t.Fatalf("initial view rendered empty runtime as healthy:\n%s", view)
	}
	if !strings.Contains(view, "refreshing") {
		t.Fatalf("initial view missing loading state:\n%s", view)
	}
}
