package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
)

// Client talks to the local runtime daemon over its Unix socket. Methods are
// thin wrappers around the runtime HTTP API and preserve server-side validation
// errors in returned error messages.
type Client struct {
	baseURL string
	http    *http.Client
}

// RuntimeError preserves structured error details returned by the runtime API
// while keeping the human-readable Error string used by existing callers.
type RuntimeError struct {
	Method         string
	Path           string
	Status         string
	StatusCode     int
	Message        string
	Code           string
	Retryable      bool
	PartialSuccess bool
}

func (e *RuntimeError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) == "" {
		return fmt.Sprintf("runtime API %s %s failed: %s", e.Method, e.Path, e.Status)
	}
	return fmt.Sprintf("runtime API %s %s failed: %s: %s", e.Method, e.Path, e.Status, e.Message)
}

// NewClient returns a client configured for the default runtime socket.
func NewClient() *Client {
	paths := DefaultPaths()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", paths.Socket)
		},
	}
	return &Client{
		baseURL: "http://agx-runtime",
		http:    &http.Client{Transport: transport},
	}
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var status Status
	if err := c.do(ctx, http.MethodGet, "/v1/status", nil, &status); err != nil {
		return Status{}, err
	}
	return status, nil
}

func (c *Client) Shutdown(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/v1/shutdown", nil, nil)
}

func (c *Client) Config(ctx context.Context) (RuntimeConfig, error) {
	var cfg RuntimeConfig
	if err := c.do(ctx, http.MethodGet, "/v1/config", nil, &cfg); err != nil {
		return RuntimeConfig{}, err
	}
	return cfg, nil
}

func (c *Client) UpdateDefaultAgent(ctx context.Context, agentName string) (RuntimeConfig, error) {
	var cfg RuntimeConfig
	req := patchConfigRequest{DefaultAgent: &agentName}
	if err := c.do(ctx, http.MethodPatch, "/v1/config", req, &cfg); err != nil {
		return RuntimeConfig{}, err
	}
	return cfg, nil
}

// Events opens the runtime server-sent event stream. The returned channel closes
// when the stream ends or ctx is canceled.
func (c *Client) Events(ctx context.Context) (<-chan Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/events", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, runtimeTransportError(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, responseRuntimeError(http.MethodGet, "/v1/events", resp)
	}
	events := make(chan Event, 32)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var event Event
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case events <- event:
			}
		}
	}()
	return events, nil
}

func (c *Client) ListAgents(ctx context.Context, projectID string) ([]Agent, error) {
	values := url.Values{}
	if strings.TrimSpace(projectID) != "" {
		values.Set("project_id", strings.TrimSpace(projectID))
	}
	return c.listAgents(ctx, values)
}

func (c *Client) ListAgentsForPath(ctx context.Context, projectPath string) ([]Agent, error) {
	values := url.Values{}
	if strings.TrimSpace(projectPath) != "" {
		values.Set("project_path", strings.TrimSpace(projectPath))
	}
	return c.listAgents(ctx, values)
}

func (c *Client) listAgents(ctx context.Context, values url.Values) ([]Agent, error) {
	path := "/v1/agents"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var agents []Agent
	if err := c.do(ctx, http.MethodGet, path, nil, &agents); err != nil {
		return nil, err
	}
	return agents, nil
}

func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var projects []Project
	if err := c.do(ctx, http.MethodGet, "/v1/projects", nil, &projects); err != nil {
		return nil, err
	}
	return projects, nil
}

func (c *Client) CreateProject(ctx context.Context, path, name string, description, defaultAgent *string) (Project, error) {
	var project Project
	req := createProjectRequest{Path: path, Name: name, Description: description, DefaultAgent: defaultAgent}
	if err := c.do(ctx, http.MethodPost, "/v1/projects", req, &project); err != nil {
		return Project{}, err
	}
	return project, nil
}

func (c *Client) GetProject(ctx context.Context, projectID string) (Project, error) {
	var project Project
	if err := c.do(ctx, http.MethodGet, "/v1/projects/"+projectID, nil, &project); err != nil {
		return Project{}, err
	}
	return project, nil
}

func (c *Client) UpdateProjectDetails(ctx context.Context, projectID, name string, description *string) (Project, error) {
	var project Project
	req := patchProjectRequest{Name: &name, Description: description}
	if err := c.do(ctx, http.MethodPatch, "/v1/projects/"+projectID, req, &project); err != nil {
		return Project{}, err
	}
	return project, nil
}

func (c *Client) UpdateProjectDefaultAgent(ctx context.Context, projectID string, defaultAgent *string) (Project, error) {
	var project Project
	req := patchProjectRequest{DefaultAgent: defaultAgent}
	if err := c.do(ctx, http.MethodPatch, "/v1/projects/"+projectID, req, &project); err != nil {
		return Project{}, err
	}
	return project, nil
}

func (c *Client) GrantProjectAccess(ctx context.Context, projectID string) (Project, error) {
	var project Project
	if err := c.do(ctx, http.MethodPost, "/v1/projects/"+projectID+"/grant-access", nil, &project); err != nil {
		return Project{}, err
	}
	return project, nil
}

func (c *Client) DeleteProject(ctx context.Context, projectID string) error {
	return c.do(ctx, http.MethodDelete, "/v1/projects/"+projectID, nil, nil)
}

func (c *Client) ListTasks(ctx context.Context, projectID string) ([]Task, error) {
	return c.ListTasksStatus(ctx, projectID, "")
}

func (c *Client) ListTasksStatus(ctx context.Context, projectID, status string) ([]Task, error) {
	var tasks []Task
	path := "/v1/tasks"
	sep := "?"
	if projectID != "" {
		path += sep + "project_id=" + projectID
		sep = "&"
	}
	if status != "" {
		path += sep + "status=" + status
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (c *Client) MonitorTasks(ctx context.Context) ([]MonitorTask, error) {
	var tasks []MonitorTask
	if err := c.do(ctx, http.MethodGet, "/v1/tasks/monitor", nil, &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (c *Client) CreateTask(ctx context.Context, projectID, title string, description *string, agentName string, allMighty bool) (Task, error) {
	return c.CreateTaskWithWorkspace(ctx, projectID, title, description, agentName, allMighty, db.WorkspaceModeWorktree)
}

// CreateTaskWithWorkspace creates an offline task without starting the agent.
func (c *Client) CreateTaskWithWorkspace(ctx context.Context, projectID, title string, description *string, agentName string, allMighty bool, workspaceMode db.WorkspaceMode) (Task, error) {
	var task Task
	req := createTaskRequest{ProjectID: projectID, Title: title, Description: description, Agent: agentName, AllMighty: allMighty, WorkspaceMode: string(workspaceMode)}
	if err := c.do(ctx, http.MethodPost, "/v1/tasks", req, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (c *Client) RunNewTask(ctx context.Context, projectID, title string, description *string, agentName string, allMighty bool) (Task, error) {
	return c.RunNewTaskWithInitialPrompt(ctx, projectID, title, description, agentName, allMighty, nil)
}

func (c *Client) RunNewTaskWithInitialPrompt(ctx context.Context, projectID, title string, description *string, agentName string, allMighty bool, initialPrompt *string) (Task, error) {
	return c.RunNewTaskWithInitialPromptWorkspace(ctx, projectID, title, description, agentName, allMighty, initialPrompt, db.WorkspaceModeWorktree)
}

// RunNewTaskWithInitialPromptWorkspace creates and immediately starts a local
// task. initialPrompt can intentionally be an empty string to suppress default
// title/description prompting.
func (c *Client) RunNewTaskWithInitialPromptWorkspace(ctx context.Context, projectID, title string, description *string, agentName string, allMighty bool, initialPrompt *string, workspaceMode db.WorkspaceMode) (Task, error) {
	var task Task
	req := createTaskRequest{ProjectID: projectID, Title: title, Description: description, Agent: agentName, AllMighty: allMighty, WorkspaceMode: string(workspaceMode), InitialPrompt: initialPrompt, RunImmediately: true}
	if err := c.do(ctx, http.MethodPost, "/v1/tasks", req, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

// RunNewDiscordTaskWithWorkspace creates a Discord-controlled task and starts
// the structured agent flow when the runtime supports the selected agent.
func (c *Client) RunNewDiscordTaskWithWorkspace(ctx context.Context, projectID, title string, description *string, agentName string, allMighty bool, workspaceMode db.WorkspaceMode) (Task, error) {
	var task Task
	req := createTaskRequest{ProjectID: projectID, Title: title, Description: description, Agent: agentName, AllMighty: allMighty, WorkspaceMode: string(workspaceMode), RunImmediately: true, Discord: true}
	if err := c.do(ctx, http.MethodPost, "/v1/tasks", req, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (c *Client) GetTask(ctx context.Context, taskID string) (Task, error) {
	var task Task
	if err := c.do(ctx, http.MethodGet, "/v1/tasks/"+taskID, nil, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (c *Client) UpdateTaskTitle(ctx context.Context, taskID, title string) (Task, error) {
	var task Task
	req := patchTaskRequest{Title: &title}
	if err := c.do(ctx, http.MethodPatch, "/v1/tasks/"+taskID, req, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

// UpdateTaskMetadata patches task metadata. description is a double pointer so
// callers can distinguish "leave unchanged" from "set nil".
func (c *Client) UpdateTaskMetadata(ctx context.Context, taskID string, title *string, description **string, clearDescription bool, agent *string) (Task, error) {
	var task Task
	req := patchTaskRequest{Title: title, Description: description, ClearDescription: clearDescription, Agent: agent}
	if err := c.do(ctx, http.MethodPatch, "/v1/tasks/"+taskID, req, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (c *Client) RunTask(ctx context.Context, taskID string) (Task, error) {
	var task Task
	if err := c.do(ctx, http.MethodPost, "/v1/tasks/"+taskID+"/run", nil, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (c *Client) StopTask(ctx context.Context, taskID string) (Task, error) {
	var task Task
	if err := c.do(ctx, http.MethodPost, "/v1/tasks/"+taskID+"/stop", nil, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (c *Client) InterruptTask(ctx context.Context, taskID string) (Task, error) {
	var task Task
	if err := c.do(ctx, http.MethodPost, "/v1/tasks/"+taskID+"/interrupt", nil, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (c *Client) DeleteTask(ctx context.Context, taskID string) error {
	return c.do(ctx, http.MethodDelete, "/v1/tasks/"+taskID, nil, nil)
}

func (c *Client) SendTaskMessage(ctx context.Context, taskID, message string) (Task, error) {
	var task Task
	if err := c.do(ctx, http.MethodPost, "/v1/tasks/"+taskID+"/message", taskMessageRequest{Message: message}, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (c *Client) RecordTaskInput(ctx context.Context, taskID, message string) (Task, error) {
	var task Task
	if err := c.do(ctx, http.MethodPost, "/v1/tasks/"+taskID+"/record-input", taskMessageRequest{Message: message}, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (c *Client) SendTaskInput(ctx context.Context, taskID, data string) error {
	return c.do(ctx, http.MethodPost, "/v1/tasks/"+taskID+"/input", taskInputRequest{Data: data}, nil)
}

func (c *Client) ResizeTaskTerminal(ctx context.Context, taskID string, cols, rows int) error {
	return c.do(ctx, http.MethodPost, "/v1/tasks/"+taskID+"/resize", taskResizeRequest{Cols: cols, Rows: rows}, nil)
}

func (c *Client) TaskLogs(ctx context.Context, taskID string, lines int) (string, error) {
	var out taskLogsResponse
	path := fmt.Sprintf("/v1/tasks/%s/logs", taskID)
	if lines > 0 {
		path = fmt.Sprintf("%s?lines=%d", path, lines)
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return "", err
	}
	return out.Logs, nil
}

// TaskLogStream streams task log events over SSE. The first event is usually a
// reset containing the initial tail, followed by append events.
func (c *Client) TaskLogStream(ctx context.Context, taskID string, lines int) (<-chan TaskLogEvent, error) {
	path := fmt.Sprintf("/v1/tasks/%s/stream", taskID)
	if lines > 0 {
		path = fmt.Sprintf("%s?lines=%d", path, lines)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, runtimeTransportError(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, responseRuntimeError(http.MethodGet, path, resp)
	}
	events := make(chan TaskLogEvent, 32)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		reader := bufio.NewReader(resp.Body)
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil && len(line) == 0 {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if !strings.HasPrefix(line, "data: ") {
				if readErr != nil {
					return
				}
				continue
			}
			var event TaskLogEvent
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
				if readErr != nil {
					return
				}
				continue
			}
			select {
			case <-ctx.Done():
				return
			case events <- event:
			}
			if readErr != nil {
				return
			}
		}
	}()
	return events, nil
}

func (c *Client) TaskTranscript(ctx context.Context, taskID string, limit int) ([]TaskTranscriptMessage, error) {
	var messages []TaskTranscriptMessage
	path := fmt.Sprintf("/v1/tasks/%s/transcript", taskID)
	if limit > 0 {
		path = fmt.Sprintf("%s?limit=%d", path, limit)
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

func (c *Client) DiscordStatus(ctx context.Context) (agxdiscord.Status, error) {
	var status agxdiscord.Status
	if err := c.do(ctx, http.MethodGet, "/v1/discord/status", nil, &status); err != nil {
		return agxdiscord.Status{}, err
	}
	return status, nil
}

func (c *Client) DiscordConnect(ctx context.Context, token, guildID, allowedUserID string) (agxdiscord.Status, error) {
	var status agxdiscord.Status
	req := discordConnectRequest{Token: token, GuildID: guildID, AllowedUserID: allowedUserID}
	if err := c.do(ctx, http.MethodPost, "/v1/discord/connect", req, &status); err != nil {
		return agxdiscord.Status{}, err
	}
	return status, nil
}

func (c *Client) DiscordDisconnect(ctx context.Context) (agxdiscord.Status, error) {
	var status agxdiscord.Status
	if err := c.do(ctx, http.MethodPost, "/v1/discord/disconnect", nil, &status); err != nil {
		return agxdiscord.Status{}, err
	}
	return status, nil
}

func (c *Client) DiscordSoftSync(ctx context.Context) (agxdiscord.Status, error) {
	var status agxdiscord.Status
	if err := c.do(ctx, http.MethodPost, "/v1/discord/soft-sync", nil, &status); err != nil {
		return agxdiscord.Status{}, err
	}
	return status, nil
}

func (c *Client) DiscordHardSync(ctx context.Context) (agxdiscord.Status, error) {
	var status agxdiscord.Status
	if err := c.do(ctx, http.MethodPost, "/v1/discord/hard-sync", nil, &status); err != nil {
		return agxdiscord.Status{}, err
	}
	return status, nil
}

func (c *Client) DiscordTaskSync(ctx context.Context, taskID string) (agxdiscord.Status, error) {
	var status agxdiscord.Status
	if err := c.do(ctx, http.MethodPost, "/v1/discord/tasks/"+taskID+"/sync", nil, &status); err != nil {
		return agxdiscord.Status{}, err
	}
	return status, nil
}

func (c *Client) DiscordInviteURL(ctx context.Context, token string) (string, error) {
	var out discordInviteResponse
	if err := c.do(ctx, http.MethodPost, "/v1/discord/invite-url", discordInviteRequest{Token: token}, &out); err != nil {
		return "", err
	}
	return out.URL, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var payload *bytes.Reader
	if body == nil {
		payload = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return runtimeTransportError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseRuntimeError(method, path, resp)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func runtimeTransportError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("agx runtime request timed out: %w", err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("agx runtime request timed out: %w", err)
	}
	return fmt.Errorf("agx runtime is not reachable: %w", err)
}

func responseRuntimeError(method, path string, resp *http.Response) error {
	message, code, retryable, partialSuccess := responseErrorDetails(resp)
	return &RuntimeError{
		Method:         method,
		Path:           path,
		Status:         resp.Status,
		StatusCode:     resp.StatusCode,
		Message:        message,
		Code:           code,
		Retryable:      retryable,
		PartialSuccess: partialSuccess,
	}
}

func responseErrorDetails(resp *http.Response) (message, code string, retryable, partialSuccess bool) {
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil || len(data) == 0 {
		return resp.Status, "", false, false
	}
	var body errorResponse
	if err := json.Unmarshal(data, &body); err == nil && strings.TrimSpace(body.Error) != "" {
		return strings.TrimSpace(body.Error), body.Code, body.Retryable, body.PartialSuccess
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return resp.Status, "", false, false
	}
	return text, "", false, false
}
