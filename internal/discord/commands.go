package discord

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/nashory/agx/internal/agent"
	"github.com/nashory/agx/internal/agentstream"
	"github.com/nashory/agx/internal/config"
)

var ErrChannelNotLinked = errors.New("discord channel is not linked to an AGX task")
var ErrTaskNotLive = errors.New("AGX task session is not running")

type TaskNotLiveError struct {
	TaskID string
	Status string
	Agent  string
}

func (e TaskNotLiveError) Error() string {
	status := strings.TrimSpace(e.Status)
	if status == "" {
		status = "unknown"
	}
	return fmt.Sprintf("%s: status=%s", ErrTaskNotLive, status)
}

func (e TaskNotLiveError) Unwrap() error {
	return ErrTaskNotLive
}

type CommandService interface {
	ListTasks(ctx context.Context) ([]TaskSummary, error)
	ListProjects(ctx context.Context) ([]ProjectSummary, error)
	CreateProject(ctx context.Context, path, name, defaultAgent string) (ProjectSummary, error)
	DeleteProject(ctx context.Context, projectRef string) (ProjectSummary, error)
	CreateTask(ctx context.Context, projectRef, title, prompt, agentName, workspaceMode string, allMighty bool) (TaskSummary, error)
	DeleteTask(ctx context.Context, taskRef string) (TaskSummary, error)
	IsControlChannel(ctx context.Context, channelID string) (bool, error)
	SoftSync(ctx context.Context) error
	HardSync(ctx context.Context, preserveControlChannelID string) error
	ResetEverything(ctx context.Context) (ResetSummary, error)
	ResolveTaskByChannel(ctx context.Context, channelID string) (string, error)
	ResolveTask(ctx context.Context, ref string) (TaskSummary, error)
	GetTask(ctx context.Context, taskID string) (TaskSummary, error)
	InterruptTask(ctx context.Context, taskID string) error
	KillTask(ctx context.Context, taskID, channelID string) error
	TaskLogs(ctx context.Context, taskID string, lines int) (string, error)
	SendTaskMessage(ctx context.Context, taskID string, message IncomingTaskMessage) (SendTaskMessageResult, error)
}

type SyncStatusService interface {
	SyncStatus(ctx context.Context) SyncStatusSummary
}

type SyncStatusSummary struct {
	Running     bool       `json:"running"`
	Kind        string     `json:"kind,omitempty"`
	Stage       string     `json:"stage,omitempty"`
	SyncID      string     `json:"syncId,omitempty"`
	Priority    string     `json:"priority,omitempty"`
	TaskID      string     `json:"taskId,omitempty"`
	CurrentStep string     `json:"currentStep,omitempty"`
	ElapsedMs   int64      `json:"elapsedMs,omitempty"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	Error       string     `json:"error,omitempty"`
}

type TaskSummary struct {
	ID               string
	Title            string
	ProjectName      string
	Status           string
	Agent            string
	AllMighty        bool
	Interface        string
	SessionName      *string
	ChannelID        string
	AgentThreadID    *string
	AgentEventCursor *string
	AgentStreamKind  *string
}

type ProjectSummary struct {
	ID   string
	Name string
	Path string
}

// ResetSummary reports what a reset-everything operation removed.
type ResetSummary struct {
	Projects int
	Tasks    int
}

type CommandInput struct {
	Name        string
	Subcommand  string
	GuildID     string
	ChannelID   string
	ChannelName string
	UserID      string
	Options     map[string]string
}

type CommandResponse struct {
	Content   string
	Ephemeral bool
	React     bool
}

func CommandInputFromInteraction(i *discordgo.InteractionCreate) CommandInput {
	data := i.ApplicationCommandData()
	input := CommandInput{
		Name:      data.Name,
		GuildID:   i.GuildID,
		ChannelID: i.ChannelID,
		Options:   map[string]string{},
	}
	if i.Member != nil {
		if i.Member.User != nil {
			input.UserID = i.Member.User.ID
		}
	}
	if input.UserID == "" && i.User != nil {
		input.UserID = i.User.ID
	}
	for _, option := range data.Options {
		if option.Type == discordgo.ApplicationCommandOptionSubCommand {
			input.Subcommand = option.Name
			for _, nested := range option.Options {
				input.Options[nested.Name] = optionString(nested)
			}
			continue
		}
		input.Options[option.Name] = optionString(option)
	}
	return input
}

type CommandRouter struct {
	cfg     config.DiscordConfig
	service CommandService
}

func NewCommandRouter(cfg config.DiscordConfig, service CommandService) *CommandRouter {
	return &CommandRouter{cfg: cfg, service: service}
}

// rejectUnauthorized returns the standard refusal response. The reason and
// requester ids are logged by IsAuthorized, the single chokepoint every entry
// point calls.
func (r *CommandRouter) rejectUnauthorized(input CommandInput) CommandResponse {
	return CommandResponse{Content: "You are not allowed to control AGX from Discord.", Ephemeral: true}
}

// IsAuthorized reports whether a request may control AGX. It logs the reason on
// denial (guild mismatch vs user not in the allowlist) with the requester's ids,
// so an otherwise opaque "not allowed" is diagnosable from the runtime logs.
func (r *CommandRouter) IsAuthorized(input CommandInput) bool {
	if strings.TrimSpace(r.cfg.GuildID) != "" && strings.TrimSpace(input.GuildID) != strings.TrimSpace(r.cfg.GuildID) {
		log.Printf("operation=%q reason=%q command=%q request_guild=%q configured_guild=%q user=%q",
			"discord_auth_denied", "guild_mismatch", input.Name, input.GuildID, r.cfg.GuildID, input.UserID)
		return false
	}
	if !IsAuthorized(r.cfg, input.UserID) {
		log.Printf("operation=%q reason=%q command=%q user=%q allowed_users=%v",
			"discord_auth_denied", "user_not_allowed", input.Name, input.UserID, r.cfg.AllowedUserIDs)
		return false
	}
	return true
}

// agentChoices exposes the known agents as slash-command choices so `/task
// create` offers a picklist instead of a free-text field where the operator has
// to guess a valid agent name. Discord allows up to 25 choices; KnownAgents is
// far smaller.
func agentChoices() []*discordgo.ApplicationCommandOptionChoice {
	agents := agent.KnownAgents()
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(agents))
	for _, a := range agents {
		label := a.Name
		if strings.TrimSpace(a.Description) != "" {
			label = fmt.Sprintf("%s (%s)", a.Name, a.Description)
		}
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: label, Value: a.Name})
	}
	return choices
}

func ApplicationCommands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{Name: "ps", Description: "List AGX tasks"},
		{Name: "soft-sync", Description: "Sync AGX projects and active tasks to Discord"},
		{Name: "hard-sync", Description: "Rebuild Discord managed channels from the current AGX state"},
		{
			Name:        "dangerously-reset-everything",
			Description: "Delete ALL AGX projects, tasks, and managed Discord channels",
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionString, Name: "confirm", Description: "Type reset to confirm this destructive action", Required: true},
			},
		},
		{
			Name:        "project",
			Description: "Manage AGX project views",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "list",
					Description: "List registered AGX projects",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "create",
					Description: "Register a git project path with AGX",
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "path", Description: "Absolute project path visible to the AGX runtime", Required: true},
						{Type: discordgo.ApplicationCommandOptionString, Name: "name", Description: "Display name", Required: false},
						{Type: discordgo.ApplicationCommandOptionString, Name: "agent", Description: "Default agent", Required: false},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "delete",
					Description: "Delete an AGX project and its tasks",
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "project", Description: "Project name, id, or absolute path", Required: true},
					},
				},
			},
		},
		{
			Name:        "task",
			Description: "Manage AGX task views",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "list",
					Description: "List current AGX tasks",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "create",
					Description: "Create a Discord-controlled AGX task",
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "project", Description: "Project name, id, or absolute path", Required: true},
						{Type: discordgo.ApplicationCommandOptionString, Name: "title", Description: "Task title", Required: true},
						{Type: discordgo.ApplicationCommandOptionString, Name: "prompt", Description: "Initial prompt", Required: false},
						{Type: discordgo.ApplicationCommandOptionString, Name: "agent", Description: "Agent (defaults to the project's default)", Required: false, Choices: agentChoices()},
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "workspace-mode",
							Description: "Workspace mode",
							Required:    false,
							Choices: []*discordgo.ApplicationCommandOptionChoice{
								{Name: "worktree", Value: "worktree"},
								{Name: "project", Value: "project"},
							},
						},
						{Type: discordgo.ApplicationCommandOptionBoolean, Name: "all-mighty", Description: "Grant unrestricted agent permissions", Required: false},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "delete",
					Description: "Delete an AGX task and its Discord channel",
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "task", Description: "Task id or id prefix", Required: true},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "logs",
					Description: "Show recent output for an AGX task",
					Options: []*discordgo.ApplicationCommandOption{
						{Type: discordgo.ApplicationCommandOptionString, Name: "task", Description: "Task id, id prefix, or title", Required: true},
						{Type: discordgo.ApplicationCommandOptionInteger, Name: "lines", Description: "Number of lines", Required: false},
					},
				},
			},
		},
		{Name: "interrupt", Description: "Interrupt the current AGX task turn"},
		{Name: "clear", Description: "Clear the current AGX task agent context"},
		{Name: "kill", Description: "Delete this AGX task and remove this task channel"},
		{
			Name:        "status",
			Description: "Show AGX task status",
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionString, Name: "task", Description: "Task ID", Required: true},
			},
		},
		{
			Name:        "logs",
			Description: "Show recent output for this AGX task channel",
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionInteger, Name: "lines", Description: "Number of lines", Required: false},
			},
		},
		{Name: "heartbeat", Description: "Check AGX health for this channel"},
		{Name: "help", Description: "Show AGX command help"},
	}
}

func (r *CommandRouter) Execute(ctx context.Context, input CommandInput) (CommandResponse, error) {
	if r.service == nil {
		return CommandResponse{}, fmt.Errorf("discord command service is not configured")
	}
	if !r.IsAuthorized(input) {
		return r.rejectUnauthorized(input), nil
	}
	allowedChannel, channelMessage, err := r.IsAllowedSlashChannel(ctx, input)
	if err != nil {
		return CommandResponse{}, err
	}
	if !allowedChannel {
		return CommandResponse{Content: channelMessage, Ephemeral: true}, nil
	}
	switch input.Name {
	case "ps":
		return r.ps(ctx)
	case "soft-sync":
		return r.softSync(ctx)
	case "hard-sync":
		return r.hardSync(ctx, input)
	case "dangerously-reset-everything":
		return r.resetEverything(ctx, input)
	case "project":
		switch input.Subcommand {
		case "list":
			return r.projectList(ctx)
		case "create":
			return r.projectCreate(ctx, input)
		case "delete":
			return r.projectDelete(ctx, input)
		}
		return CommandResponse{}, fmt.Errorf("unknown project command %q", input.Subcommand)
	case "task":
		switch input.Subcommand {
		case "list":
			return r.taskList(ctx)
		case "create":
			return r.taskCreate(ctx, input)
		case "delete":
			return r.taskDelete(ctx, input)
		case "logs":
			return r.logs(ctx, input)
		}
		return CommandResponse{}, fmt.Errorf("unknown task command %q", input.Subcommand)
	case "interrupt":
		return r.taskAction(ctx, input, "interrupted", r.service.InterruptTask)
	case "clear":
		return r.clearTaskContext(ctx, input)
	case "kill":
		return r.killTask(ctx, input)
	case "status":
		return r.status(ctx, input)
	case "logs":
		return r.logs(ctx, input)
	case "heartbeat":
		return r.heartbeat(ctx, input)
	case "help":
		return CommandResponse{Content: commandHelp()}, nil
	default:
		return CommandResponse{}, fmt.Errorf("unknown command %q", input.Name)
	}
}

func (r *CommandRouter) IsAllowedSlashChannel(ctx context.Context, input CommandInput) (bool, string, error) {
	if strings.TrimSpace(r.cfg.GuildID) == "" {
		return true, "", nil
	}
	isControl, err := r.isControlChannel(ctx, input)
	if err != nil {
		return false, "", err
	}
	if isHeartbeatCommand(input.Name) {
		if isControl {
			return true, "", nil
		}
		taskID, err := r.service.ResolveTaskByChannel(ctx, input.ChannelID)
		if err != nil || strings.TrimSpace(taskID) == "" {
			return false, "Use `/heartbeat` in an AGX task channel or `#agx-control`.", nil
		}
		return true, "", nil
	}
	if isTaskOnlyCommand(input.Name) {
		if isControl {
			return false, "Use this command in an AGX task channel.", nil
		}
		taskID, err := r.service.ResolveTaskByChannel(ctx, input.ChannelID)
		if err != nil || strings.TrimSpace(taskID) == "" {
			return false, "Use this command in an AGX task channel.", nil
		}
		return true, "", nil
	}
	if isControl {
		return true, "", nil
	}
	return false, "Use AGX management slash commands in `#agx-control`.", nil
}

func (r *CommandRouter) isControlChannel(ctx context.Context, input CommandInput) (bool, error) {
	if strings.EqualFold(strings.TrimSpace(input.ChannelName), controlChannelName) {
		return true, nil
	}
	if r.service == nil {
		return false, fmt.Errorf("discord command service is not configured")
	}
	return r.service.IsControlChannel(ctx, input.ChannelID)
}

func isTaskOnlyCommand(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "interrupt", "clear", "kill", "logs":
		return true
	default:
		return false
	}
}

func isHeartbeatCommand(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), "heartbeat")
}

func (r *CommandRouter) HandlePlainMessage(ctx context.Context, input CommandInput, message string) (CommandResponse, error) {
	if r.service == nil {
		return CommandResponse{}, fmt.Errorf("discord command service is not configured")
	}
	if !r.IsAuthorized(input) {
		return r.rejectUnauthorized(input), nil
	}
	taskID, err := r.taskID(ctx, input)
	if err != nil {
		return CommandResponse{}, err
	}
	if isPlainKillMessage(message) {
		if err := r.service.KillTask(ctx, taskID, input.ChannelID); err != nil {
			if isPartialSuccessError(err) {
				return CommandResponse{Content: err.Error()}, nil
			}
			return CommandResponse{}, err
		}
		return CommandResponse{Content: fmt.Sprintf("Task `%s` killed; deleting this task channel.", shortID(taskID))}, nil
	}
	return r.handlePlainTaskMessage(ctx, taskID, IncomingTaskMessage{Text: message})
}

func (r *CommandRouter) HandleComponentChoice(ctx context.Context, input CommandInput, taskID, choice string) (CommandResponse, error) {
	if r.service == nil {
		return CommandResponse{}, fmt.Errorf("discord command service is not configured")
	}
	if !r.IsAuthorized(input) {
		return r.rejectUnauthorized(input), nil
	}
	taskID = strings.TrimSpace(taskID)
	choice = strings.TrimSpace(choice)
	if taskID == "" {
		return CommandResponse{}, fmt.Errorf("AGX choice is missing a task id")
	}
	if choice == "" {
		return CommandResponse{}, fmt.Errorf("AGX choice is empty")
	}
	linkedTaskID, err := r.service.ResolveTaskByChannel(ctx, input.ChannelID)
	if err != nil {
		return CommandResponse{}, err
	}
	if linkedTaskID != taskID {
		return CommandResponse{}, fmt.Errorf("this choice belongs to a different AGX task")
	}
	return r.handlePlainTaskMessage(ctx, taskID, IncomingTaskMessage{Text: choice})
}

func (r *CommandRouter) killTask(ctx context.Context, input CommandInput) (CommandResponse, error) {
	taskID, err := r.taskID(ctx, input)
	if err != nil {
		return CommandResponse{}, err
	}
	if err := r.service.KillTask(ctx, taskID, input.ChannelID); err != nil {
		if isPartialSuccessError(err) {
			return CommandResponse{Content: err.Error()}, nil
		}
		return CommandResponse{}, err
	}
	return CommandResponse{Content: fmt.Sprintf("Task `%s` killed; deleting this task channel.", shortID(taskID))}, nil
}

func isPartialSuccessError(err error) bool {
	var partial interface{ PartialSuccess() bool }
	return errors.As(err, &partial) && partial.PartialSuccess()
}

func (r *CommandRouter) clearTaskContext(ctx context.Context, input CommandInput) (CommandResponse, error) {
	taskID, err := r.taskID(ctx, input)
	if err != nil {
		return CommandResponse{}, err
	}
	response, err := r.handlePlainTaskMessage(ctx, taskID, IncomingTaskMessage{Text: "/clear"})
	if err != nil {
		return CommandResponse{}, err
	}
	if strings.TrimSpace(response.Content) != "" {
		return response, nil
	}
	return CommandResponse{Content: fmt.Sprintf("Task `%s` context cleared.", shortID(taskID))}, nil
}

func (r *CommandRouter) handlePlainTaskMessage(ctx context.Context, taskID string, message IncomingTaskMessage) (CommandResponse, error) {
	result, err := r.service.SendTaskMessage(ctx, taskID, message)
	if err != nil {
		if agentstream.IsUnsupported(err) {
			task, taskErr := r.service.GetTask(ctx, taskID)
			if taskErr != nil {
				return CommandResponse{Content: "This agent does not support structured Discord streaming yet.\nOpen the task in AGX Desktop, or use `/logs` for a terminal snapshot."}, nil
			}
			return CommandResponse{Content: NewSemanticRenderer().Unsupported(toAgentStreamTask(task)).Content}, nil
		}
		var notLive TaskNotLiveError
		if errors.As(err, &notLive) {
			return CommandResponse{Content: taskNotLiveMessage(notLive)}, nil
		}
		return CommandResponse{}, err
	}
	if strings.TrimSpace(result.Notice) != "" {
		return CommandResponse{Content: result.Notice, React: true}, nil
	}
	task, err := r.service.GetTask(ctx, taskID)
	if err == nil && !isStructuredStreamTask(task) {
		return CommandResponse{Content: fmt.Sprintf("Message sent to %s. Open AGX Desktop to follow progress for this task.", displayAgentName(task.Agent)), React: true}, nil
	}
	return CommandResponse{React: true}, nil
}

func taskNotLiveMessage(err TaskNotLiveError) string {
	status := strings.TrimSpace(err.Status)
	if status == "" {
		status = "unknown"
	}
	return fmt.Sprintf("Task session appears to have stopped (`%s`). Open AGX Desktop to inspect this task, then send your message here again.", status)
}

func displayAgentName(agent string) string {
	agent = strings.TrimSpace(agent)
	if agent == "" {
		return "the agent"
	}
	return agent
}

func (r *CommandRouter) HandleReaction(ctx context.Context, input CommandInput, emoji string) (CommandResponse, error) {
	if r.service == nil {
		return CommandResponse{}, fmt.Errorf("discord command service is not configured")
	}
	if !isInterruptEmoji(emoji) {
		return CommandResponse{}, nil
	}
	if !r.IsAuthorized(input) {
		return r.rejectUnauthorized(input), nil
	}
	taskID, err := r.taskID(ctx, input)
	if err != nil {
		return CommandResponse{}, err
	}
	if err := r.service.InterruptTask(ctx, taskID); err != nil {
		if agentstream.IsUnsupported(err) {
			task, taskErr := r.service.GetTask(ctx, taskID)
			if taskErr != nil {
				return CommandResponse{Content: "This agent does not support structured Discord interrupt yet."}, nil
			}
			return CommandResponse{Content: fmt.Sprintf("Discord interrupt is not available for `%s` yet.\nOpen the task in AGX Desktop to interrupt it.", task.Agent)}, nil
		}
		return CommandResponse{}, err
	}
	return CommandResponse{Content: fmt.Sprintf("Interrupted task `%s`.", shortID(taskID))}, nil
}

func (r *CommandRouter) ps(ctx context.Context) (CommandResponse, error) {
	return r.taskList(ctx)
}

func (r *CommandRouter) taskList(ctx context.Context) (CommandResponse, error) {
	tasks, err := r.service.ListTasks(ctx)
	if err != nil {
		return CommandResponse{}, err
	}
	if len(tasks) == 0 {
		return CommandResponse{Content: "No AGX tasks are running."}, nil
	}
	var b strings.Builder
	b.WriteString("AGX tasks:\n")
	for _, task := range tasks {
		sessionState := "offline"
		if isLiveTaskStatus(task.Status) && (task.SessionName != nil || isStructuredStreamTask(task)) {
			sessionState = "live"
		}
		mode := "standard"
		if task.AllMighty {
			mode = "all-mighty"
		}
		iface := strings.TrimSpace(task.Interface)
		if iface == "" {
			iface = "local"
		}
		fmt.Fprintf(&b, "- `%s` %-8s %-7s %-8s %-10s %-8s %s", shortID(task.ID), task.Status, iface, task.Agent, mode, sessionState, task.Title)
		if task.ProjectName != "" {
			fmt.Fprintf(&b, " (%s)", task.ProjectName)
		}
		if task.ChannelID != "" {
			fmt.Fprintf(&b, " <#%s>", task.ChannelID)
		}
		b.WriteByte('\n')
	}
	return CommandResponse{Content: strings.TrimSpace(b.String())}, nil
}

func (r *CommandRouter) projectList(ctx context.Context) (CommandResponse, error) {
	projects, err := r.service.ListProjects(ctx)
	if err != nil {
		return CommandResponse{}, err
	}
	if len(projects) == 0 {
		return CommandResponse{Content: "No AGX projects are registered."}, nil
	}
	var b strings.Builder
	b.WriteString("AGX projects:\n")
	for _, project := range projects {
		fmt.Fprintf(&b, "- `%s` %s\n", project.Name, project.Path)
	}
	return CommandResponse{Content: strings.TrimSpace(b.String())}, nil
}

func (r *CommandRouter) projectCreate(ctx context.Context, input CommandInput) (CommandResponse, error) {
	project, err := r.service.CreateProject(ctx, input.Options["path"], input.Options["name"], input.Options["agent"])
	if err != nil {
		return CommandResponse{}, err
	}
	return CommandResponse{Content: fmt.Sprintf("Project `%s` registered at `%s`.", project.Name, project.Path)}, nil
}

func (r *CommandRouter) projectDelete(ctx context.Context, input CommandInput) (CommandResponse, error) {
	project, err := r.service.DeleteProject(ctx, input.Options["project"])
	if err != nil {
		return CommandResponse{}, err
	}
	return CommandResponse{Content: fmt.Sprintf("Project `%s` deleted.", project.Name)}, nil
}

func (r *CommandRouter) taskCreate(ctx context.Context, input CommandInput) (CommandResponse, error) {
	allMighty := true
	if raw := strings.TrimSpace(input.Options["all-mighty"]); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return CommandResponse{}, fmt.Errorf("all-mighty must be true or false")
		}
		allMighty = parsed
	}
	task, err := r.service.CreateTask(ctx,
		input.Options["project"],
		input.Options["title"],
		input.Options["prompt"],
		input.Options["agent"],
		input.Options["workspace-mode"],
		allMighty,
	)
	if err != nil {
		return CommandResponse{}, err
	}
	channel := ""
	if strings.TrimSpace(task.ChannelID) != "" {
		channel = fmt.Sprintf(" <#%s>", task.ChannelID)
	}
	return CommandResponse{Content: fmt.Sprintf("Task `%s` created in `%s`.%s", shortID(task.ID), valueOrUnknown(task.ProjectName), channel)}, nil
}

func (r *CommandRouter) taskDelete(ctx context.Context, input CommandInput) (CommandResponse, error) {
	task, err := r.service.DeleteTask(ctx, input.Options["task"])
	if err != nil {
		if isPartialSuccessError(err) {
			return CommandResponse{Content: err.Error()}, nil
		}
		return CommandResponse{}, err
	}
	return CommandResponse{Content: fmt.Sprintf("Task `%s` deleted.", shortID(task.ID))}, nil
}

func (r *CommandRouter) softSync(ctx context.Context) (CommandResponse, error) {
	if err := r.service.SoftSync(ctx); err != nil {
		return CommandResponse{}, err
	}
	return CommandResponse{Content: "Soft sync completed."}, nil
}

func (r *CommandRouter) resetEverything(ctx context.Context, input CommandInput) (CommandResponse, error) {
	if !strings.EqualFold(strings.TrimSpace(input.Options["confirm"]), "reset") {
		return CommandResponse{Content: "Reset aborted. Re-run `/dangerously-reset-everything` with `confirm: reset` to wipe all AGX projects, tasks, and managed channels.", Ephemeral: true}, nil
	}
	summary, err := r.service.ResetEverything(ctx)
	if err != nil {
		return CommandResponse{}, err
	}
	return CommandResponse{Content: fmt.Sprintf("Reset complete: removed %d project(s) and %d task(s). Rebuilding managed Discord channels.", summary.Projects, summary.Tasks)}, nil
}

func (r *CommandRouter) hardSync(ctx context.Context, input CommandInput) (CommandResponse, error) {
	if err := r.service.HardSync(ctx, input.ChannelID); err != nil {
		return CommandResponse{}, err
	}
	return CommandResponse{Content: "Hard sync started. Use `/heartbeat` in `#agx-control` to check progress."}, nil
}

func (r *CommandRouter) taskAction(ctx context.Context, input CommandInput, verb string, fn func(context.Context, string) error) (CommandResponse, error) {
	taskID, err := r.taskID(ctx, input)
	if err != nil {
		return CommandResponse{}, err
	}
	if err := fn(ctx, taskID); err != nil {
		return CommandResponse{}, err
	}
	return CommandResponse{Content: fmt.Sprintf("Task `%s` %s.", shortID(taskID), verb)}, nil
}

func (r *CommandRouter) status(ctx context.Context, input CommandInput) (CommandResponse, error) {
	taskID, err := r.taskID(ctx, input)
	if err != nil {
		return CommandResponse{}, err
	}
	task, err := r.service.GetTask(ctx, taskID)
	if err != nil {
		return CommandResponse{}, err
	}
	return CommandResponse{Content: fmt.Sprintf("`%s` %s %s - %s", shortID(task.ID), task.Status, task.Agent, task.Title)}, nil
}

func (r *CommandRouter) logs(ctx context.Context, input CommandInput) (CommandResponse, error) {
	taskID, err := r.logsTaskID(ctx, input)
	if err != nil {
		return CommandResponse{}, err
	}
	lines := 50
	if raw := strings.TrimSpace(input.Options["lines"]); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return CommandResponse{}, fmt.Errorf("lines must be a positive integer")
		}
		if parsed > 500 {
			parsed = 500
		}
		lines = parsed
	}
	logs, err := r.service.TaskLogs(ctx, taskID, lines)
	if err != nil {
		return CommandResponse{}, err
	}
	if strings.TrimSpace(logs) == "" {
		logs = "(no output captured)"
	}
	return CommandResponse{Content: FormatLogOutputMessage(logs)}, nil
}

func (r *CommandRouter) heartbeat(ctx context.Context, input CommandInput) (CommandResponse, error) {
	isControl, err := r.isControlChannel(ctx, input)
	if err != nil {
		return CommandResponse{}, err
	}
	if isControl {
		projects, err := r.service.ListProjects(ctx)
		if err != nil {
			return CommandResponse{}, err
		}
		tasks, err := r.service.ListTasks(ctx)
		if err != nil {
			return CommandResponse{}, err
		}
		syncStatus := SyncStatusSummary{}
		if syncService, ok := r.service.(SyncStatusService); ok {
			syncStatus = syncService.SyncStatus(ctx)
		}
		return CommandResponse{Content: renderBackendHeartbeat(projects, tasks, syncStatus)}, nil
	}
	taskID, err := r.service.ResolveTaskByChannel(ctx, input.ChannelID)
	if err != nil || strings.TrimSpace(taskID) == "" {
		return CommandResponse{Content: "This channel is not linked to an AGX task.", Ephemeral: true}, nil
	}
	task, err := r.service.GetTask(ctx, taskID)
	if err != nil {
		return CommandResponse{}, err
	}
	return CommandResponse{Content: renderHeartbeat(task)}, nil
}

func renderBackendHeartbeat(projects []ProjectSummary, tasks []TaskSummary, syncStatus SyncStatusSummary) string {
	statusCounts := map[string]int{}
	liveSessions := 0
	for _, task := range tasks {
		status := strings.TrimSpace(task.Status)
		if status == "" {
			status = "unknown"
		}
		statusCounts[status]++
		if isLiveTaskStatus(task.Status) && (task.SessionName != nil || isStructuredStreamTask(task)) {
			liveSessions++
		}
	}
	lines := []string{
		"AGX backend heartbeat",
		"- Status: ok",
		fmt.Sprintf("- Projects: %d", len(projects)),
		fmt.Sprintf("- Tasks: %d", len(tasks)),
		fmt.Sprintf("- Live agent sessions: %d", liveSessions),
	}
	switch {
	case syncStatus.Running:
		lines = append(lines, fmt.Sprintf("- Sync: running %s (%s)", valueOrUnknown(syncStatus.Kind), valueOrUnknown(syncStatus.Stage)))
	case strings.TrimSpace(syncStatus.Error) != "":
		lines = append(lines, fmt.Sprintf("- Sync: failed (%s)", syncStatus.Error))
	case strings.TrimSpace(syncStatus.Stage) != "":
		lines = append(lines, fmt.Sprintf("- Sync: %s", syncStatus.Stage))
	}
	for _, status := range []string{"active", "waiting", "complete", "offline", "unknown"} {
		if count := statusCounts[status]; count > 0 {
			lines = append(lines, fmt.Sprintf("- %s tasks: %d", status, count))
		}
	}
	return strings.Join(lines, "\n")
}

func renderHeartbeat(task TaskSummary) string {
	sessionState := "offline"
	if isLiveTaskStatus(task.Status) && (task.SessionName != nil || isStructuredStreamTask(task)) {
		sessionState = "live"
	}
	mode := "standard"
	if task.AllMighty {
		mode = "all-mighty"
	}
	lines := []string{
		"AGX heartbeat",
		fmt.Sprintf("- Task: `%s` %s", shortID(task.ID), strings.TrimSpace(task.Title)),
		fmt.Sprintf("- Project: %s", valueOrUnknown(task.ProjectName)),
		fmt.Sprintf("- Status: %s", valueOrUnknown(task.Status)),
		fmt.Sprintf("- Interface: %s", valueOrUnknown(task.Interface)),
		fmt.Sprintf("- Agent: %s", valueOrUnknown(task.Agent)),
		fmt.Sprintf("- Mode: %s", mode),
		fmt.Sprintf("- Agent session: %s", sessionState),
	}
	if task.ChannelID != "" {
		lines = append(lines, fmt.Sprintf("- Channel: <#%s>", task.ChannelID))
	}
	return strings.Join(lines, "\n")
}

func valueOrUnknown(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func (r *CommandRouter) taskID(ctx context.Context, input CommandInput) (string, error) {
	if taskID := strings.TrimSpace(input.Options["task"]); taskID != "" {
		return taskID, nil
	}
	if isControlChannelName(input.ChannelName) {
		return "", fmt.Errorf("task id is required in #agx-control")
	}
	taskID, err := r.service.ResolveTaskByChannel(ctx, input.ChannelID)
	if err != nil || strings.TrimSpace(taskID) == "" {
		return "", ErrChannelNotLinked
	}
	return taskID, nil
}

// logsTaskID resolves the task whose logs should be shown. An explicit `task`
// option (used by `/task logs` in #agx-control) is resolved by id, id prefix, or
// title; otherwise the task is inferred from the current task channel so a bare
// `/logs` works in a task channel.
func (r *CommandRouter) logsTaskID(ctx context.Context, input CommandInput) (string, error) {
	if ref := strings.TrimSpace(input.Options["task"]); ref != "" {
		task, err := r.service.ResolveTask(ctx, ref)
		if err != nil {
			return "", err
		}
		return task.ID, nil
	}
	taskID, err := r.service.ResolveTaskByChannel(ctx, input.ChannelID)
	if err != nil || strings.TrimSpace(taskID) == "" {
		return "", ErrChannelNotLinked
	}
	return taskID, nil
}

func isControlChannelName(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), controlChannelName)
}

func commandHelp() string {
	return strings.Join([]string{
		"`/task list` or `/ps` - list current tasks",
		"`/task create`, `/task delete` - create or delete Discord-controlled tasks from `#agx-control`",
		"`/project list`, `/project create`, `/project delete` - manage registered projects from `#agx-control`",
		"`/soft-sync` - sync Discord to the current AGX state",
		"`/hard-sync` - rebuild managed Discord channels from AGX state",
		"`/dangerously-reset-everything confirm:reset` - delete ALL projects, tasks, and managed channels",
		"`/status task:<id>`, `/task logs task:<name-or-id>` - inspect tasks from `#agx-control`",
		"`/logs` - show recent output in an AGX task channel (crash/error diagnosis)",
		"`/interrupt` - interrupt the current task turn in an AGX task channel",
		"`/clear` - clear the current task agent context in an AGX task channel",
		"`/kill` - delete the current task and remove its Discord task channel",
		"`/heartbeat` - check task health in a task channel, or backend health in `#agx-control`",
		"Management commands run in `#agx-control`.",
	}, "\n")
}

func isPlainKillMessage(message string) bool {
	return strings.EqualFold(strings.TrimSpace(message), "/kill")
}

func codeBlock(text string) string {
	text = strings.ReplaceAll(text, "```", "`\u200b``")
	return "```\n" + text + "\n```"
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func isInterruptEmoji(emoji string) bool {
	switch strings.TrimSpace(emoji) {
	case "🛑", "⏹", "⏹️", "✋", "🖐", "🖐️":
		return true
	default:
		return false
	}
}

func optionString(option *discordgo.ApplicationCommandInteractionDataOption) string {
	switch option.Type {
	case discordgo.ApplicationCommandOptionInteger:
		return strconv.FormatInt(option.IntValue(), 10)
	case discordgo.ApplicationCommandOptionBoolean:
		return strconv.FormatBool(option.BoolValue())
	default:
		return option.StringValue()
	}
}
