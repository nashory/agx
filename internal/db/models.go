package db

import (
	"fmt"
	"strings"
	"time"
)

// Project is a registered source repository that can own AGX tasks.
type Project struct {
	ID           string
	Name         string
	Path         string
	Description  *string
	DefaultAgent *string
	LastOpened   time.Time
	CreatedAt    time.Time
}

// TaskStatus is the persisted lifecycle state exposed to CLI, desktop, runtime,
// and Discord clients.
type TaskStatus string

const (
	StatusActive   TaskStatus = "active"
	StatusWaiting  TaskStatus = "waiting"
	StatusComplete TaskStatus = "complete"
	StatusOffline  TaskStatus = "offline"
)

var ValidTaskStatuses = []TaskStatus{
	StatusActive,
	StatusWaiting,
	StatusComplete,
	StatusOffline,
}

// ParseTaskStatus normalizes and validates a task status string.
func ParseTaskStatus(s string) (TaskStatus, error) {
	status := TaskStatus(strings.ToLower(strings.TrimSpace(s)))
	if IsValidTaskStatus(status) {
		return status, nil
	}
	return "", fmt.Errorf("invalid task status %q; valid statuses: %s", s, TaskStatusList())
}

func IsValidTaskStatus(status TaskStatus) bool {
	for _, valid := range ValidTaskStatuses {
		if status == valid {
			return true
		}
	}
	return false
}

func TaskStatusList() string {
	values := make([]string, 0, len(ValidTaskStatuses))
	for _, status := range ValidTaskStatuses {
		values = append(values, string(status))
	}
	return strings.Join(values, ", ")
}

// TaskInterface identifies whether a task is controlled locally or through
// Discord.
type TaskInterface string

const (
	TaskInterfaceLocal   TaskInterface = "local"
	TaskInterfaceDiscord TaskInterface = "discord"
)

var ValidTaskInterfaces = []TaskInterface{
	TaskInterfaceLocal,
	TaskInterfaceDiscord,
}

func IsValidTaskInterface(iface TaskInterface) bool {
	for _, valid := range ValidTaskInterfaces {
		if iface == valid {
			return true
		}
	}
	return false
}

// WorkspaceMode controls whether a task runs in an isolated git worktree or
// directly in the project checkout.
type WorkspaceMode string

const (
	WorkspaceModeWorktree WorkspaceMode = "worktree"
	WorkspaceModeProject  WorkspaceMode = "project"
)

var ValidWorkspaceModes = []WorkspaceMode{
	WorkspaceModeWorktree,
	WorkspaceModeProject,
}

// ParseWorkspaceMode normalizes and validates a workspace mode string.
func ParseWorkspaceMode(s string) (WorkspaceMode, error) {
	mode := WorkspaceMode(strings.ToLower(strings.TrimSpace(s)))
	if IsValidWorkspaceMode(mode) {
		return mode, nil
	}
	return "", fmt.Errorf("invalid workspace mode %q; valid modes: %s", s, WorkspaceModeList())
}

func IsValidWorkspaceMode(mode WorkspaceMode) bool {
	for _, valid := range ValidWorkspaceModes {
		if mode == valid {
			return true
		}
	}
	return false
}

func WorkspaceModeList() string {
	values := make([]string, 0, len(ValidWorkspaceModes))
	for _, mode := range ValidWorkspaceModes {
		values = append(values, string(mode))
	}
	return strings.Join(values, ", ")
}

// Task is the durable task record. Runtime-only fields such as SessionName,
// WorktreePath, and AgentThreadID are intentionally persisted so AGX can recover
// or clean them up after process restarts.
type Task struct {
	ID               string
	ProjectID        string
	Title            string
	Description      *string
	LastUserPrompt   *string
	Interface        TaskInterface
	WorkspaceMode    WorkspaceMode
	Status           TaskStatus
	Agent            string
	AllMighty        bool
	SessionName      *string
	WorktreePath     *string
	BranchName       *string
	BaseBranch       *string
	AgentThreadID    *string
	AgentEventCursor *string
	AgentStreamKind  *string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// LiveTask joins a live task with project metadata used by monitor and recovery
// views.
type LiveTask struct {
	Task
	ProjectName string
	ProjectPath string
}

// TaskTranscriptMessage stores user/assistant conversation history for
// structured agent tasks.
type TaskTranscriptMessage struct {
	ID               int64
	TaskID           string
	TurnID           *string
	Role             string
	Body             string
	DiscordMessageID *string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// TaskAttachment records a Discord attachment that AGX persisted for a task.
// Files live on disk under the runtime attachment root; this row is the durable
// index used for idempotency, transcript rendering, and cleanup.
type TaskAttachment struct {
	ID                  string
	TaskID              string
	DiscordMessageID    string
	DiscordAttachmentID string
	Filename            string
	ContentType         string
	SizeBytes           int64
	LocalPath           string
	SourceURL           string
	SHA256              string
	CreatedAt           time.Time
}

// DiscordMapping records the durable relationship between AGX objects and
// Discord channels/categories/messages.
type DiscordMapping struct {
	ID          string
	AGXType     string
	AGXID       string
	DiscordType string
	DiscordID   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type DiscordTaskSyncStatus string

const (
	DiscordTaskSyncPending DiscordTaskSyncStatus = "pending"
	DiscordTaskSyncSynced  DiscordTaskSyncStatus = "synced"
	DiscordTaskSyncFailed  DiscordTaskSyncStatus = "failed"
)

// DiscordTaskSyncState records the last known Discord channel sync result for
// one AGX task. It complements discord_mappings with retry and failure context.
type DiscordTaskSyncState struct {
	TaskID           string
	Status           DiscordTaskSyncStatus
	Attempts         int
	DiscordChannelID *string
	DiscordThreadID  *string
	LastSuccessAt    *time.Time
	LastFailureAt    *time.Time
	LastError        *string
	RetryAfter       *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
