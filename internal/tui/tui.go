package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	agxdiscord "github.com/nashory/agx/internal/discord"
	"github.com/nashory/agx/internal/display"
	agxruntime "github.com/nashory/agx/internal/runtime"
)

const defaultRefreshInterval = 2 * time.Second

// RuntimeClient is the subset of the runtime API used by the terminal UI.
type RuntimeClient interface {
	Status(context.Context) (agxruntime.Status, error)
	ListProjects(context.Context) ([]agxruntime.Project, error)
	MonitorTasks(context.Context) ([]agxruntime.MonitorTask, error)
	ListTasks(context.Context, string) ([]agxruntime.Task, error)
	DiscordStatus(context.Context) (agxdiscord.Status, error)
}

// Options controls interactive TUI behavior.
type Options struct {
	RefreshInterval time.Duration
}

// Snapshot is a point-in-time view of runtime state rendered by the TUI.
type Snapshot struct {
	Runtime     agxruntime.Status
	RuntimeErr  error
	Projects    []agxruntime.Project
	ActiveTasks []agxruntime.MonitorTask
	RecentTasks []agxruntime.Task
	Discord     agxdiscord.Status
	DiscordErr  error
	ProjectsErr error
	ActiveErr   error
	RecentErr   error
	RefreshedAt time.Time
}

// FetchSnapshot gathers the runtime data needed by the TUI. It keeps partial
// results and records per-section errors so the UI can stay useful while one
// endpoint is temporarily unhealthy.
func FetchSnapshot(ctx context.Context, client RuntimeClient) Snapshot {
	snapshot := Snapshot{RefreshedAt: time.Now()}
	status, err := client.Status(ctx)
	if err != nil {
		snapshot.RuntimeErr = err
		return snapshot
	}
	snapshot.Runtime = status
	snapshot.Projects, snapshot.ProjectsErr = client.ListProjects(ctx)
	snapshot.ActiveTasks, snapshot.ActiveErr = client.MonitorTasks(ctx)
	snapshot.RecentTasks, snapshot.RecentErr = client.ListTasks(ctx, "")
	snapshot.Discord, snapshot.DiscordErr = client.DiscordStatus(ctx)
	return snapshot
}

// WriteSnapshot renders a single noninteractive TUI snapshot.
func WriteSnapshot(w io.Writer, snapshot Snapshot) {
	fmt.Fprint(w, RenderSnapshot(snapshot, 100))
}

// RenderSnapshot returns a terminal-friendly text representation of a snapshot.
func RenderSnapshot(snapshot Snapshot, width int) string {
	if width <= 0 {
		width = 100
	}
	var b strings.Builder
	fmt.Fprintf(&b, "AGX TUI\n")
	fmt.Fprintf(&b, "refreshed: %s\n\n", snapshot.RefreshedAt.Format(time.RFC3339))
	if snapshot.RuntimeErr != nil {
		fmt.Fprintf(&b, "runtime: offline (%v)\n", snapshot.RuntimeErr)
		fmt.Fprintln(&b, "start it with: agx runtime start")
		return b.String()
	}
	fmt.Fprintf(&b, "runtime: ok pid=%d uptime=%ds\n", snapshot.Runtime.PID, snapshot.Runtime.UptimeSeconds)
	fmt.Fprintf(&b, "socket: %s\n", snapshot.Runtime.SocketPath)
	fmt.Fprintf(&b, "projects: %s\n", sectionCount(len(snapshot.Projects), snapshot.ProjectsErr))
	fmt.Fprintf(&b, "discord: %s\n\n", discordSummary(snapshot.Discord, snapshot.DiscordErr))

	fmt.Fprintln(&b, "Active Tasks")
	if snapshot.ActiveErr != nil {
		fmt.Fprintf(&b, "  unavailable: %v\n", snapshot.ActiveErr)
	} else if len(snapshot.ActiveTasks) == 0 {
		fmt.Fprintln(&b, "  none")
	} else {
		for _, task := range snapshot.ActiveTasks {
			fmt.Fprintf(&b, "  %-8s %-14s %-10s %-10s %s\n",
				display.ShortID(task.ID),
				display.Truncate(task.ProjectName, 14),
				task.Agent,
				task.Status,
				display.Truncate(task.Title, max(24, width-50)),
			)
		}
	}

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Recent Tasks")
	if snapshot.RecentErr != nil {
		fmt.Fprintf(&b, "  unavailable: %v\n", snapshot.RecentErr)
	} else if len(snapshot.RecentTasks) == 0 {
		fmt.Fprintln(&b, "  none")
	} else {
		for _, task := range firstTasks(snapshot.RecentTasks, 10) {
			fmt.Fprintf(&b, "  %-8s %-10s %-10s %-7s %s\n",
				display.ShortID(task.ID),
				task.Status,
				task.Agent,
				display.Age(task.CreatedAt),
				display.Truncate(task.Title, max(24, width-44)),
			)
		}
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "keys: r refresh, q quit")
	return b.String()
}

// Run starts the interactive terminal UI.
func Run(ctx context.Context, client RuntimeClient, opts Options) error {
	if opts.RefreshInterval <= 0 {
		opts.RefreshInterval = defaultRefreshInterval
	}
	_, err := tea.NewProgram(
		newModel(ctx, client, opts.RefreshInterval),
		tea.WithAltScreen(),
		tea.WithContext(ctx),
	).Run()
	return err
}

type model struct {
	ctx             context.Context
	client          RuntimeClient
	refreshInterval time.Duration
	snapshot        Snapshot
	hasSnapshot     bool
	width           int
	height          int
	loading         bool
}

type snapshotMsg Snapshot

func newModel(ctx context.Context, client RuntimeClient, refreshInterval time.Duration) model {
	return model{ctx: ctx, client: client, refreshInterval: refreshInterval, width: 100, loading: true}
}

func (m model) Init() tea.Cmd {
	return m.fetch()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "r":
			m.loading = true
			return m, m.fetch()
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case snapshotMsg:
		m.snapshot = Snapshot(msg)
		m.hasSnapshot = true
		m.loading = false
		return m, tea.Tick(m.refreshInterval, func(time.Time) tea.Msg {
			return refreshTickMsg{}
		})
	case refreshTickMsg:
		m.loading = true
		return m, m.fetch()
	}
	return m, nil
}

func (m model) View() string {
	if !m.hasSnapshot {
		return "AGX TUI\n\nrefreshing...\n"
	}
	view := RenderSnapshot(m.snapshot, m.width)
	if m.loading {
		view += "\nrefreshing...\n"
	}
	return view
}

func (m model) fetch() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
		defer cancel()
		return snapshotMsg(FetchSnapshot(ctx, m.client))
	}
}

type refreshTickMsg struct{}

func sectionCount(count int, err error) string {
	if err != nil {
		return "unavailable (" + err.Error() + ")"
	}
	return fmt.Sprintf("%d", count)
}

func discordSummary(status agxdiscord.Status, err error) string {
	if err != nil {
		return "unavailable (" + err.Error() + ")"
	}
	if !status.Enabled {
		return "disabled"
	}
	if status.Connected {
		if status.GuildName != "" {
			return "connected to " + status.GuildName
		}
		return "connected"
	}
	if status.Error != "" {
		return "enabled, disconnected (" + status.Error + ")"
	}
	return "enabled, disconnected"
}

func firstTasks(tasks []agxruntime.Task, limit int) []agxruntime.Task {
	if len(tasks) <= limit {
		return tasks
	}
	return tasks[:limit]
}
