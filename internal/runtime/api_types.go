package runtime

import (
	"time"

	"github.com/nashory/agx/internal/db"
)

// Project is the JSON representation of a registered repository returned by the
// runtime API.
type Project struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Path          string    `json:"path"`
	Description   *string   `json:"description,omitempty"`
	DefaultAgent  *string   `json:"defaultAgent,omitempty"`
	AccessGranted bool      `json:"accessGranted"`
	AccessError   *string   `json:"accessError,omitempty"`
	LastOpened    time.Time `json:"lastOpened"`
	CreatedAt     time.Time `json:"createdAt"`
}

// Task is the JSON representation of a persisted task returned by the runtime
// API.
type Task struct {
	ID               string        `json:"id"`
	ProjectID        string        `json:"projectId"`
	Title            string        `json:"title"`
	Description      *string       `json:"description,omitempty"`
	LastUserPrompt   *string       `json:"lastUserPrompt,omitempty"`
	Interface        string        `json:"interface"`
	Status           db.TaskStatus `json:"status"`
	Agent            string        `json:"agent"`
	AllMighty        bool          `json:"allMighty"`
	WorkspaceMode    string        `json:"workspaceMode"`
	SessionName      *string       `json:"sessionName,omitempty"`
	WorktreePath     *string       `json:"worktreePath,omitempty"`
	BranchName       *string       `json:"branchName,omitempty"`
	BaseBranch       *string       `json:"baseBranch,omitempty"`
	AgentThreadID    *string       `json:"agentThreadId,omitempty"`
	AgentEventCursor *string       `json:"agentEventCursor,omitempty"`
	AgentStreamKind  *string       `json:"agentStreamKind,omitempty"`
	DiscordSync      *DiscordSync  `json:"discordSync,omitempty"`
	CreatedAt        time.Time     `json:"createdAt"`
	UpdatedAt        time.Time     `json:"updatedAt"`
}

type DiscordSync struct {
	Status           string     `json:"status"`
	Attempts         int        `json:"attempts"`
	DiscordChannelID *string    `json:"discordChannelId,omitempty"`
	LastSuccessAt    *time.Time `json:"lastSuccessAt,omitempty"`
	LastFailureAt    *time.Time `json:"lastFailureAt,omitempty"`
	LastError        *string    `json:"lastError,omitempty"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

// MonitorTask augments a live task with project context for monitor clients.
type MonitorTask struct {
	Task
	ProjectName string `json:"projectName"`
	ProjectPath string `json:"projectPath"`
}

// TaskTranscriptMessage is a JSON transcript entry for structured tasks.
type TaskTranscriptMessage struct {
	ID        int64     `json:"id"`
	TaskID    string    `json:"taskId"`
	TurnID    *string   `json:"turnId,omitempty"`
	Role      string    `json:"role"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Agent is a JSON-safe view of an agent registry entry.
type Agent struct {
	Name        string `json:"name"`
	Command     string `json:"command"`
	Description string `json:"description"`
	Available   bool   `json:"available"`
}

type RuntimeConfig struct {
	DefaultAgent string         `json:"defaultAgent"`
	VoiceSTT     VoiceSTTConfig `json:"voiceStt"`
}

type VoiceSTTConfig struct {
	Mode        string `json:"mode"`
	FFmpegPath  string `json:"ffmpegPath"`
	WhisperPath string `json:"whisperPath"`
	ModelPath   string `json:"modelPath"`
	Language    string `json:"language"`
	Timeout     string `json:"timeout"`
}

type patchConfigRequest struct {
	DefaultAgent *string `json:"defaultAgent"`
	VoiceSTT     *VoiceSTTConfig `json:"voiceStt"`
}

type createProjectRequest struct {
	Path         string  `json:"path"`
	Name         string  `json:"name"`
	Description  *string `json:"description"`
	DefaultAgent *string `json:"defaultAgent"`
}

type patchProjectRequest struct {
	Name         *string `json:"name"`
	Description  *string `json:"description"`
	DefaultAgent *string `json:"defaultAgent"`
}

type createTaskRequest struct {
	ProjectID      string  `json:"projectId"`
	Title          string  `json:"title"`
	Description    *string `json:"description"`
	Agent          string  `json:"agent"`
	AllMighty      bool    `json:"allMighty"`
	WorkspaceMode  string  `json:"workspaceMode"`
	InitialPrompt  *string `json:"initialPrompt"`
	RunImmediately bool    `json:"runImmediately"`
	Discord        bool    `json:"discord"`
}

type patchTaskRequest struct {
	Title            *string  `json:"title"`
	Description      **string `json:"description"`
	ClearDescription bool     `json:"clearDescription"`
	Agent            *string  `json:"agent"`
}

type taskMessageRequest struct {
	Message string `json:"message"`
}

type taskInputRequest struct {
	Data string `json:"data"`
}

type taskResizeRequest struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

type taskLogsResponse struct {
	Logs string `json:"logs"`
}

// TaskLogEvent is streamed as server-sent events for task log updates.
type TaskLogEvent struct {
	TaskID string `json:"taskId"`
	Data   string `json:"data,omitempty"`
	Reset  bool   `json:"reset"`
	Error  string `json:"error,omitempty"`
}

type discordConnectRequest struct {
	Token         string `json:"token"`
	GuildID       string `json:"guildId"`
	AllowedUserID string `json:"allowedUserId"`
}

type discordInviteRequest struct {
	Token string `json:"token"`
}

type discordInviteResponse struct {
	URL string `json:"url"`
}
