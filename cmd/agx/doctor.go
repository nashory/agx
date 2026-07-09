package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nashory/agx/internal/agent"
	"github.com/nashory/agx/internal/config"
	agxruntime "github.com/nashory/agx/internal/runtime"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose AGX runtime prerequisites",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
			defer cancel()
			client := agxruntime.NewClient()
			if status, err := client.Status(ctx); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "runtime: ok pid=%d uptime=%ds\n", status.PID, status.UptimeSeconds)
				fmt.Fprintf(cmd.OutOrStdout(), "transport: %s\n", transportOrSocket(status))
				fmt.Fprintf(cmd.OutOrStdout(), "lock: %s\n", status.LockPath)
				if discord, err := client.DiscordStatus(ctx); err == nil {
					fmt.Fprintf(cmd.OutOrStdout(), "runtime discord: enabled=%t connected=%t guild=%s\n", discord.Enabled, discord.Connected, redactedSetting(discord.GuildID))
					if discord.Error != "" {
						fmt.Fprintf(cmd.OutOrStdout(), "runtime discord error: %s\n", discord.Error)
					}
				}
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "runtime: not running (%v)\n", err)
				fmt.Fprintf(cmd.OutOrStdout(), "socket: %s\n", agxruntime.DefaultPaths().Socket)
				fmt.Fprintf(cmd.OutOrStdout(), "lock: %s\n", agxruntime.DefaultPaths().Lock)
			}
			dir := config.ConfigDir()
			fmt.Fprintf(cmd.OutOrStdout(), "config dir: %s\n", dir)
			fmt.Fprintf(cmd.OutOrStdout(), "database: %s\n", filepath.Join(dir, "agx.db"))
			if info, err := os.Stat(dir); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "config dir mode: %s\n", info.Mode().Perm())
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "config dir mode: unavailable (%v)\n", err)
			}
			if stats, err := agxruntime.AttachmentStorageStats(agxruntime.DefaultPaths()); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "attachments: %d files, %s\n", stats.Files, byteCount(stats.Bytes))
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "attachments: unavailable (%v)\n", err)
			}
			printRuntimeService(cmd)
			printTool(cmd, "tmux")
			printTool(cmd, "git")
			cfg, warnings := config.LoadGlobal()
			for _, warning := range warnings {
				fmt.Fprintf(cmd.OutOrStdout(), "config warning: %v\n", warning)
			}
			registry := agent.NewRegistry(cfg.DefaultAgent, agent.FromConfig(cfg)...)
			fmt.Fprintf(cmd.OutOrStdout(), "default agent: %s\n", registry.DefaultName())
			for _, ag := range registry.All() {
				path, err := exec.LookPath(ag.Command)
				status := "missing"
				if err == nil {
					status = path
				}
				fmt.Fprintf(cmd.OutOrStdout(), "agent %-12s %s\n", ag.Name+":", status)
			}
			discord := cfg.Discord
			fmt.Fprintf(cmd.OutOrStdout(), "discord enabled: %t\n", discord.Enabled)
			fmt.Fprintf(cmd.OutOrStdout(), "discord guild: %s\n", redactedSetting(discord.GuildID))
			fmt.Fprintf(cmd.OutOrStdout(), "discord allowed user: %s\n", redactedSetting(firstConfiguredUser(discord.AllowedUserIDs)))
			fmt.Fprintf(cmd.OutOrStdout(), "discord token: %s\n", redactedSetting(discord.BotToken))
			fmt.Fprintf(cmd.OutOrStdout(), "PATH: %s\n", compactPath(os.Getenv("PATH")))
			return nil
		},
	}
}

func byteCount(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func printTool(cmd *cobra.Command, name string) {
	path, err := exec.LookPath(name)
	if err != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: missing\n", name)
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", name, path)
}

func printRuntimeService(cmd *cobra.Command) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	printRuntimeServiceStatus(cmd, agxruntime.CurrentRuntimeServiceManager().Status(ctx))
	stdoutPath, stderrPath := agxruntime.RuntimeLogPaths()
	fmt.Fprintf(cmd.OutOrStdout(), "runtime log: %s\n", stdoutPath)
	fmt.Fprintf(cmd.OutOrStdout(), "runtime error log: %s\n", stderrPath)
}

func printRuntimeServiceStatus(cmd *cobra.Command, status agxruntime.RuntimeServiceStatus) {
	label := status.PathLabel
	if label == "" {
		label = "runtime service"
	}
	if status.Path != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s)\n", label, status.Path, status.State)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", label, status.State)
	}
	if status.Detail != "" {
		manager := status.Manager
		if manager == "" {
			manager = label
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s detail: %s\n", manager, status.Detail)
		return
	}
}

func compactPath(path string) string {
	parts := strings.Split(path, ":")
	if len(parts) <= 8 {
		return path
	}
	return strings.Join(parts[:8], ":") + ":..."
}
