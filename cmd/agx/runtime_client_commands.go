package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nashory/agx/internal/db"
	"github.com/nashory/agx/internal/display"
	agxruntime "github.com/nashory/agx/internal/runtime"
	"github.com/nashory/agx/internal/tmux"
	agxtui "github.com/nashory/agx/internal/tui"
	"github.com/spf13/cobra"
)

func isRuntimeBackedInvocation(args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch args[1] {
	case "run", "ps", "logs", "send", "stop", "interrupt", "attach", "chat", "attachment":
		return true
	case "agent":
		return len(args) >= 3 && args[2] == "list"
	case "task":
		return len(args) >= 3 && (args[2] == "create" || args[2] == "list" || args[2] == "show" || args[2] == "edit" || args[2] == "delete")
	case "project":
		return len(args) >= 3 && (args[2] == "init" || args[2] == "list" || args[2] == "delete" || args[2] == "config")
	default:
		return false
	}
}

func executeRuntimeBackedCommand() {
	client := agxruntime.NewClient()
	rootCmd := &cobra.Command{
		Use:           "agx",
		Short:         "Run and manage local coding agents through the AGX runtime",
		Version:       versionString(),
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("run `agx tui`, `agx ps`, or `agx --help`")
		},
	}
	rootCmd.AddCommand(
		newRuntimeClientRunCmd(client),
		newRuntimeClientPsCmd(client),
		newRuntimeClientLogsCmd(client),
		newRuntimeClientSendCmd(client),
		newRuntimeClientStopCmd(client),
		newRuntimeClientInterruptCmd(client),
		newRuntimeClientAttachCmd(client),
		newRuntimeClientTaskCmd(client),
		newRuntimeClientProjectCmd(client),
		newRuntimeClientAgentCmd(client),
		newRuntimeClientChatCmd(client),
		newRuntimeClientAttachmentCmd(),
		newRuntimeCmd(),
		newDoctorCmd(),
		newRuntimeClientTuiCmd(client),
	)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(exitCodeFor(err))
	}
}

func newRuntimeClientAttachmentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attachment",
		Short: "Manage persisted Discord attachments",
	}
	cmd.AddCommand(
		newRuntimeClientAttachmentPruneCmd(),
		newRuntimeClientAttachmentListCmd(),
	)
	return cmd
}

func newRuntimeClientAttachmentPruneCmd() *cobra.Command {
	var olderThan time.Duration
	var taskID string
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete expired or task-scoped Discord attachments",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := agxruntime.PruneAttachments(agxruntime.AttachmentPruneOptions{OlderThan: olderThan, TaskID: taskID})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted %d attachment rows, %d files, %s\n", result.AttachmentsDeleted, result.FilesDeleted, byteCount(result.BytesDeleted))
			return nil
		},
	}
	cmd.Flags().DurationVar(&olderThan, "older-than", 7*24*time.Hour, "prune completed/offline task attachments older than this duration")
	cmd.Flags().StringVar(&taskID, "task", "", "delete all attachments for a task")
	return cmd
}

func newRuntimeClientAttachmentListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show persisted Discord attachment storage usage",
		RunE: func(cmd *cobra.Command, args []string) error {
			stats, err := agxruntime.AttachmentStorageStats(agxruntime.DefaultPaths())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "root: %s\n", stats.Root)
			fmt.Fprintf(cmd.OutOrStdout(), "files: %d\n", stats.Files)
			fmt.Fprintf(cmd.OutOrStdout(), "bytes: %s\n", byteCount(stats.Bytes))
			return nil
		},
	}
}

func newRuntimeClientTuiCmd(client *agxruntime.Client) *cobra.Command {
	var dashboard bool
	var once bool
	var refresh time.Duration
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Open the AGX terminal UI",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := runtimeCLIContext(cmd)
			defer cancel()
			if once {
				agxtui.WriteSnapshot(cmd.OutOrStdout(), agxtui.FetchSnapshot(ctx, client))
				return nil
			}
			_ = dashboard
			return agxtui.Run(cmd.Context(), client, agxtui.Options{RefreshInterval: refresh})
		},
	}
	cmd.Flags().BoolVarP(&dashboard, "dashboard", "g", false, "open the dashboard view")
	cmd.Flags().BoolVar(&once, "once", false, "print one TUI snapshot and exit")
	cmd.Flags().DurationVar(&refresh, "refresh", 2*time.Second, "interactive refresh interval")
	return cmd
}

func newRuntimeClientAgentCmd(client *agxruntime.Client) *cobra.Command {
	cmd := &cobra.Command{Use: "agent", Short: "Inspect agent CLIs"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List known agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectRoot, _ := findGitRoot(".")
			ctx, cancel := runtimeCLIContext(cmd)
			defer cancel()
			agents, err := client.ListAgentsForPath(ctx, projectRoot)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-10s %-11s %s\n", "AGENT", "STATUS", "COMMAND")
			for _, ag := range agents {
				status := "not found"
				if ag.Available {
					status = "available"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-10s %-11s %s\n", ag.Name, status, ag.Command)
			}
			return nil
		},
	})
	return cmd
}

func newRuntimeClientAttachCmd(client *agxruntime.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "attach TASK_ID",
		Short: "Attach to a task tmux window",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := runtimeCLIContext(cmd)
			defer cancel()
			task, err := client.GetTask(ctx, args[0])
			if err != nil {
				return err
			}
			if task.SessionName == nil || strings.TrimSpace(*task.SessionName) == "" {
				return fmt.Errorf("task has no active tmux session")
			}
			project, err := client.GetProject(ctx, task.ProjectID)
			if err != nil {
				return err
			}
			sessionName := tmux.SafeSessionName(project.Name + "-" + display.ShortID(project.ID))
			return tmux.NewController().Attach(tmux.Target(sessionName, *task.SessionName))
		},
	}
}

func newRuntimeClientChatCmd(client *agxruntime.Client) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Configure Discord chat integration",
	}
	cmd.AddCommand(
		newRuntimeClientChatConnectCmd(client),
		newRuntimeClientChatDisconnectCmd(client),
		newRuntimeClientChatStatusCmd(client),
		newRuntimeClientChatSyncCmd(client),
	)
	return cmd
}

func newRuntimeClientChatConnectCmd(client *agxruntime.Client) *cobra.Command {
	var token string
	var guild string
	var allowedUsers []string
	cmd := &cobra.Command{
		Use:   "connect",
		Short: "Save Discord connection settings and connect runtime",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := runtimeCLIContext(cmd)
			defer cancel()
			status, err := client.DiscordConnect(ctx, token, guild, firstConfiguredUser(allowedUsers))
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "discord chat enabled for guild %s\n", status.GuildID)
			return nil
		},
	}
	cmd.Flags().StringVar(&token, "token", "", "Discord bot token")
	cmd.Flags().StringVar(&guild, "guild", "", "Discord guild/server ID")
	cmd.Flags().StringArrayVar(&allowedUsers, "allow-user", nil, "Discord user ID allowed to control AGX; only the first value is used")
	return cmd
}

func newRuntimeClientChatDisconnectCmd(client *agxruntime.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "disconnect",
		Short: "Disable Discord chat integration",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := runtimeCLIContext(cmd)
			defer cancel()
			if _, err := client.DiscordDisconnect(ctx); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "discord chat disabled")
			return nil
		},
	}
}

func newRuntimeClientChatStatusCmd(client *agxruntime.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show Discord chat runtime status",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := runtimeCLIContext(cmd)
			defer cancel()
			status, err := client.DiscordStatus(ctx)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "enabled: %t\n", status.Enabled)
			fmt.Fprintf(cmd.OutOrStdout(), "connected: %t\n", status.Connected)
			fmt.Fprintf(cmd.OutOrStdout(), "guild: %s\n", emptyPlaceholder(status.GuildID))
			fmt.Fprintf(cmd.OutOrStdout(), "guild name: %s\n", emptyPlaceholder(status.GuildName))
			fmt.Fprintf(cmd.OutOrStdout(), "allowed user: %s\n", emptyPlaceholder(firstConfiguredUser(status.AllowedUserIDs)))
			if status.Error != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "error: %s\n", status.Error)
			}
			return nil
		},
	}
}

func newRuntimeClientChatSyncCmd(client *agxruntime.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Reconcile Discord chat state",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := runtimeCLIContext(cmd)
			defer cancel()
			status, err := client.DiscordSoftSync(ctx)
			if err != nil {
				return err
			}
			if status.GuildName != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "discord chat synced with %s\n", status.GuildName)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "discord chat synced with guild %s\n", status.GuildID)
			return nil
		},
	}
}

func newRuntimeClientProjectCmd(client *agxruntime.Client) *cobra.Command {
	cmd := &cobra.Command{Use: "project", Short: "Manage projects"}
	var agentName string
	configCmd := &cobra.Command{
		Use:   "config PROJECT",
		Short: "Set project config in the database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := runtimeCLIContext(cmd)
			defer cancel()
			project, err := resolveRuntimeProject(ctx, client, args[0], false)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("agent") {
				if _, err := client.UpdateProjectDefaultAgent(ctx, project.ID, &agentName); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "project %s default agent set to %s\n", project.Name, agentName)
			}
			return nil
		},
	}
	configCmd.Flags().StringVar(&agentName, "agent", "", "default agent name")
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List projects",
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx, cancel := runtimeCLIContext(cmd)
				defer cancel()
				projects, err := client.ListProjects(ctx)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-16s %-10s %s\n", "NAME", "AGENT", "PATH")
				for _, project := range projects {
					agent := ""
					if project.DefaultAgent != nil {
						agent = *project.DefaultAgent
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%-16s %-10s %s\n", project.Name, agent, project.Path)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "init",
			Short: "Register the current git repository",
			RunE: func(cmd *cobra.Command, args []string) error {
				root, err := findGitRoot(".")
				if err != nil {
					return fmt.Errorf("not inside a git repository")
				}
				ctx, cancel := runtimeCLIContext(cmd)
				defer cancel()
				project, err := client.CreateProject(ctx, root, filepath.Base(root), nil, nil)
				if err != nil {
					return err
				}
				granted, err := client.GrantProjectAccess(ctx, project.ID)
				if err != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "project %s registered at %s\n", project.Name, project.Path)
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: project access validation failed: %v\n", err)
					return nil
				}
				project = granted
				fmt.Fprintf(cmd.OutOrStdout(), "project %s registered at %s\n", project.Name, project.Path)
				return nil
			},
		},
		&cobra.Command{
			Use:   "delete PROJECT",
			Short: "Delete a project and its tasks",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx, cancel := runtimeCLIContext(cmd)
				defer cancel()
				project, err := resolveRuntimeProject(ctx, client, args[0], false)
				if err != nil {
					return err
				}
				if err := client.DeleteProject(ctx, project.ID); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "deleted project %s\n", project.Name)
				return nil
			},
		},
		configCmd,
	)
	return cmd
}

func newRuntimeClientTaskCmd(client *agxruntime.Client) *cobra.Command {
	cmd := &cobra.Command{Use: "task", Short: "Manage tasks"}
	cmd.AddCommand(
		newRuntimeClientTaskCreateCmd(client),
		newRuntimeClientTaskListCmd(client),
		newRuntimeClientTaskShowCmd(client),
		newRuntimeClientTaskEditCmd(client),
		newRuntimeClientTaskDeleteCmd(client),
	)
	return cmd
}

func newRuntimeClientTaskCreateCmd(client *agxruntime.Client) *cobra.Command {
	var agentName string
	var projectRef string
	var description string
	cmd := &cobra.Command{
		Use:   "create TITLE",
		Short: "Create and run a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := runtimeCLIContext(cmd)
			defer cancel()
			project, err := resolveRuntimeProject(ctx, client, projectRef, true)
			if err != nil {
				return err
			}
			var desc *string
			if description != "" {
				desc = &description
			}
			task, err := client.RunNewTask(ctx, project.ID, args[0], desc, agentName, true)
			if err != nil {
				return err
			}
			agent := agentName
			if agent == "" {
				agent = task.Agent
			}
			fmt.Fprintf(cmd.OutOrStdout(), "task %s active (%s)\n", display.ShortID(task.ID), agent)
			return nil
		},
	}
	cmd.Flags().StringVarP(&agentName, "agent", "a", "", "agent")
	cmd.Flags().StringVarP(&projectRef, "project", "p", "", "project")
	cmd.Flags().StringVar(&description, "description", "", "detailed prompt")
	return cmd
}

func newRuntimeClientTaskListCmd(client *agxruntime.Client) *cobra.Command {
	var projectRef string
	var status string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := runtimeCLIContext(cmd)
			defer cancel()
			var projectID string
			if projectRef != "" {
				project, err := resolveRuntimeProject(ctx, client, projectRef, false)
				if err != nil {
					return err
				}
				projectID = project.ID
			} else if project, err := resolveRuntimeProject(ctx, client, "", false); err == nil {
				projectID = project.ID
			}
			tasks, err := client.ListTasksStatus(ctx, projectID, status)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-8s %-10s %-10s %s\n", "ID", "STATUS", "AGENT", "TITLE")
			for _, task := range tasks {
				fmt.Fprintf(cmd.OutOrStdout(), "%-8s %-10s %-10s %s\n", display.ShortID(task.ID), task.Status, task.Agent, task.Title)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&projectRef, "project", "p", "", "project")
	cmd.Flags().StringVarP(&status, "status", "s", "", "status: "+db.TaskStatusList())
	return cmd
}

func newRuntimeClientTaskShowCmd(client *agxruntime.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "show TASK_ID",
		Short: "Show task details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := runtimeCLIContext(cmd)
			defer cancel()
			task, err := resolveRuntimeTask(ctx, client, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "ID:          %s\n", task.ID)
			fmt.Fprintf(cmd.OutOrStdout(), "Title:       %s\n", task.Title)
			fmt.Fprintf(cmd.OutOrStdout(), "Status:      %s\n", task.Status)
			fmt.Fprintf(cmd.OutOrStdout(), "Agent:       %s\n", task.Agent)
			if task.Description != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Description: %s\n", *task.Description)
			}
			if task.SessionName != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Session:     %s\n", *task.SessionName)
			}
			if task.WorktreePath != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Worktree:    %s\n", *task.WorktreePath)
			}
			if task.BranchName != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Branch:      %s\n", *task.BranchName)
			}
			return nil
		},
	}
}

func newRuntimeClientTaskEditCmd(client *agxruntime.Client) *cobra.Command {
	var title string
	var description string
	var clearDescription bool
	var agentName string
	cmd := &cobra.Command{
		Use:   "edit TASK_ID",
		Short: "Edit task metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := runtimeCLIContext(cmd)
			defer cancel()
			task, err := client.GetTask(ctx, args[0])
			if err != nil {
				return err
			}
			var titlePtr *string
			if cmd.Flags().Changed("title") {
				titlePtr = &title
			}
			var descPtr **string
			if cmd.Flags().Changed("description") {
				desc := &description
				descPtr = &desc
			}
			var agentPtr *string
			if cmd.Flags().Changed("agent") {
				agentPtr = &agentName
			}
			if _, err := client.UpdateTaskMetadata(ctx, task.ID, titlePtr, descPtr, clearDescription, agentPtr); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "updated %s\n", display.ShortID(task.ID))
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "title")
	cmd.Flags().StringVar(&description, "description", "", "description")
	cmd.Flags().BoolVar(&clearDescription, "clear-description", false, "clear description")
	cmd.Flags().StringVar(&agentName, "agent", "", "agent")
	return cmd
}

func newRuntimeClientTaskDeleteCmd(client *agxruntime.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "delete TASK_ID",
		Short: "Delete a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := runtimeCLIContext(cmd)
			defer cancel()
			task, err := resolveRuntimeTask(ctx, client, args[0])
			if err != nil {
				return err
			}
			if err := client.DeleteTask(ctx, task.ID); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", display.ShortID(task.ID))
			return nil
		},
	}
}

func newRuntimeClientRunCmd(client *agxruntime.Client) *cobra.Command {
	var agentName string
	var projectRef string
	var openTUI bool
	cmd := &cobra.Command{
		Use:   "run PROMPT",
		Short: "Create a task and run it immediately",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if openTUI {
				return fmt.Errorf("task-level TUI attach is not available yet; use `agx attach` after the task starts or open `agx tui` separately")
			}
			ctx, cancel := runtimeCLIContext(cmd)
			defer cancel()
			project, err := resolveRuntimeProject(ctx, client, projectRef, true)
			if err != nil {
				return err
			}
			task, err := client.RunNewTask(ctx, project.ID, args[0], nil, agentName, true)
			if err != nil {
				return err
			}
			agent := agentName
			if agent == "" {
				agent = task.Agent
			}
			fmt.Fprintf(cmd.OutOrStdout(), "task %s active (%s)\n", display.ShortID(task.ID), agent)
			return nil
		},
	}
	cmd.Flags().StringVarP(&agentName, "agent", "a", "", "agent to run")
	cmd.Flags().StringVarP(&projectRef, "project", "p", "", "project name or absolute path")
	cmd.Flags().BoolVarP(&openTUI, "tui", "t", false, "open the task in the terminal UI after creation")
	return cmd
}

func newRuntimeClientPsCmd(client *agxruntime.Client) *cobra.Command {
	var projectRef string
	var all bool
	cmd := &cobra.Command{
		Use:   "ps",
		Short: "List active tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := runtimeCLIContext(cmd)
			defer cancel()
			fmt.Fprintf(cmd.OutOrStdout(), "%-8s %-16s %-32s %-10s %-10s %s\n", "ID", "PROJECT", "TASK", "AGENT", "STATUS", "AGE")
			if all || projectRef != "" {
				var project agxruntime.Project
				var projectID string
				if projectRef != "" {
					resolved, err := resolveRuntimeProject(ctx, client, projectRef, false)
					if err != nil {
						return err
					}
					project = resolved
					projectID = project.ID
				}
				tasks, err := client.ListTasks(ctx, projectID)
				if err != nil {
					return err
				}
				projectsByID, _ := runtimeProjectsByID(ctx, client)
				for _, task := range tasks {
					projectName := project.Name
					if projectName == "" {
						projectName = projectsByID[task.ProjectID].Name
					}
					fmtRuntimeTask(cmd, projectName, task)
				}
				return nil
			}
			tasks, err := client.MonitorTasks(ctx)
			if err != nil {
				return err
			}
			for _, task := range tasks {
				fmtRuntimeTask(cmd, task.ProjectName, task.Task)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&projectRef, "project", "p", "", "project name or absolute path")
	cmd.Flags().BoolVar(&all, "all", false, "show all tasks")
	return cmd
}

func newRuntimeClientLogsCmd(client *agxruntime.Client) *cobra.Command {
	var lines int
	cmd := &cobra.Command{
		Use:   "logs TASK_ID",
		Short: "Show task logs from runtime",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := runtimeCLIContext(cmd)
			defer cancel()
			task, err := resolveRuntimeTask(ctx, client, args[0])
			if err != nil {
				return err
			}
			logs, err := client.TaskLogs(ctx, task.ID, lines)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), logs)
			return nil
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 0, "include scrollback lines")
	return cmd
}

func newRuntimeClientSendCmd(client *agxruntime.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "send TASK_ID MESSAGE",
		Short: "Send a message to a task",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := runtimeCLIContext(cmd)
			defer cancel()
			task, err := resolveRuntimeTask(ctx, client, args[0])
			if err != nil {
				return err
			}
			_, err = client.SendTaskMessage(ctx, task.ID, args[1])
			return err
		},
	}
}

func newRuntimeClientStopCmd(client *agxruntime.Client) *cobra.Command {
	var projectRef string
	var all bool
	cmd := &cobra.Command{
		Use:   "stop TASK_ID | --project PROJECT --all",
		Short: "Stop a task session",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := runtimeCLIContext(cmd)
			defer cancel()
			if all {
				if len(args) != 0 {
					return fmt.Errorf("TASK_ID cannot be used with --all")
				}
				project, err := resolveRuntimeProject(ctx, client, projectRef, false)
				if err != nil {
					return err
				}
				tasks, err := client.ListTasks(ctx, project.ID)
				if err != nil {
					return err
				}
				for _, task := range tasks {
					if task.Status != db.StatusActive && task.Status != db.StatusWaiting {
						continue
					}
					if _, err := client.StopTask(ctx, task.ID); err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "stopped %s\n", display.ShortID(task.ID))
				}
				return nil
			}
			if len(args) != 1 {
				return fmt.Errorf("TASK_ID is required")
			}
			task, err := resolveRuntimeTask(ctx, client, args[0])
			if err != nil {
				return err
			}
			if _, err := client.StopTask(ctx, task.ID); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "stopped %s\n", display.ShortID(task.ID))
			return nil
		},
	}
	cmd.Flags().StringVarP(&projectRef, "project", "p", "", "project name or absolute path")
	cmd.Flags().BoolVar(&all, "all", false, "stop all active/waiting task sessions in the selected project")
	return cmd
}

func newRuntimeClientInterruptCmd(client *agxruntime.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "interrupt TASK_ID",
		Short: "Interrupt a running task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := runtimeCLIContext(cmd)
			defer cancel()
			task, err := resolveRuntimeTask(ctx, client, args[0])
			if err != nil {
				return err
			}
			if _, err := client.InterruptTask(ctx, task.ID); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "interrupted %s\n", display.ShortID(task.ID))
			return nil
		},
	}
}

func runtimeCLIContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	return context.WithTimeout(cmd.Context(), 30*time.Second)
}

func resolveRuntimeProject(ctx context.Context, client *agxruntime.Client, ref string, createCurrent bool) (agxruntime.Project, error) {
	if ref == "" {
		root, err := findGitRoot(".")
		if err != nil {
			return agxruntime.Project{}, fmt.Errorf("not inside a git repository; pass --project")
		}
		projects, err := client.ListProjects(ctx)
		if err != nil {
			return agxruntime.Project{}, err
		}
		for _, project := range projects {
			if samePath(project.Path, root) {
				return project, nil
			}
		}
		if createCurrent {
			project, err := client.CreateProject(ctx, root, filepath.Base(root), nil, nil)
			if err != nil {
				return agxruntime.Project{}, err
			}
			return client.GrantProjectAccess(ctx, project.ID)
		}
		return agxruntime.Project{}, db.ErrProjectNotFound
	}
	projects, err := client.ListProjects(ctx)
	if err != nil {
		return agxruntime.Project{}, err
	}
	var matches []agxruntime.Project
	for _, project := range projects {
		if project.ID == ref || project.Name == ref || samePath(project.Path, ref) {
			matches = append(matches, project)
		}
	}
	if len(matches) == 0 {
		return agxruntime.Project{}, db.ErrProjectNotFound
	}
	if len(matches) > 1 {
		return agxruntime.Project{}, fmt.Errorf("%w: %s", db.ErrProjectAmbiguous, ref)
	}
	return matches[0], nil
}

func resolveRuntimeTask(ctx context.Context, client *agxruntime.Client, ref string) (agxruntime.Task, error) {
	tasks, err := client.ListTasks(ctx, "")
	if err != nil {
		return agxruntime.Task{}, err
	}
	var matches []agxruntime.Task
	for _, task := range tasks {
		if task.ID == ref || strings.HasPrefix(task.ID, ref) {
			matches = append(matches, task)
		}
	}
	if len(matches) == 0 {
		return agxruntime.Task{}, db.ErrTaskNotFound
	}
	if len(matches) > 1 {
		return agxruntime.Task{}, fmt.Errorf("%w: %s", db.ErrTaskAmbiguous, ref)
	}
	return matches[0], nil
}

func runtimeProjectsByID(ctx context.Context, client *agxruntime.Client) (map[string]agxruntime.Project, error) {
	projects, err := client.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]agxruntime.Project, len(projects))
	for _, project := range projects {
		out[project.ID] = project
	}
	return out, nil
}

func fmtRuntimeTask(cmd *cobra.Command, projectName string, task agxruntime.Task) {
	fmt.Fprintf(cmd.OutOrStdout(), "%-8s %-16s %-32s %-10s %-10s %s\n", display.ShortID(task.ID), projectName, display.Truncate(task.Title, 32), task.Agent, task.Status, display.Age(task.CreatedAt))
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA == nil {
		a = aa
	}
	if errB == nil {
		b = bb
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
