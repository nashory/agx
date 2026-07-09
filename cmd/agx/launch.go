package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"syscall"
	"time"

	"github.com/nashory/agx/internal/agent"
	"github.com/nashory/agx/internal/config"
	agxruntime "github.com/nashory/agx/internal/runtime"
	"github.com/spf13/cobra"
)

type launchOptions struct {
	Platform             string
	DiscordToken         string
	DiscordGuildID       string
	DiscordAllowedUserID string
	SkipDiscord          bool
	SkipDiscordSync      bool
	Wait                 time.Duration
}

type launchRunner func(context.Context, *cobra.Command, launchOptions) error

func newLaunchCmd() *cobra.Command {
	return newLaunchCmdWithRunner(runLaunch)
}

func newLaunchCmdWithRunner(runner launchRunner) *cobra.Command {
	opts := launchOptions{Wait: 20 * time.Second}
	cmd := &cobra.Command{
		Use:   "launch",
		Short: "Sanity-check and launch AGX runtime plus Discord",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), opts.Wait+60*time.Second)
			defer cancel()
			return runner(ctx, cmd, opts)
		},
	}
	cmd.Flags().StringVar(&opts.Platform, "platform", "", "target platform: windows, macos, linux (default: current host; windows means WSL2)")
	cmd.Flags().StringVar(&opts.DiscordToken, "discord-token", "", "Discord bot token; prefer DISCORD_BOT_TOKEN to avoid shell history and process args")
	cmd.Flags().StringVar(&opts.DiscordGuildID, "guild", "", "Discord guild/server ID; defaults to config.toml")
	cmd.Flags().StringVar(&opts.DiscordAllowedUserID, "allow-user", "", "Discord user ID allowed to control AGX; defaults to config.toml")
	cmd.Flags().BoolVar(&opts.SkipDiscord, "skip-discord", false, "launch runtime without connecting Discord")
	cmd.Flags().BoolVar(&opts.SkipDiscordSync, "skip-discord-sync", false, "connect Discord without running soft sync")
	cmd.Flags().DurationVar(&opts.Wait, "wait", opts.Wait, "time to wait for runtime startup")
	return cmd
}

func runLaunch(ctx context.Context, cmd *cobra.Command, opts launchOptions) error {
	platform, err := normalizeLaunchPlatform(opts.Platform, goruntime.GOOS)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "launch platform: %s\n", platform)
	if platform == "windows" {
		fmt.Fprintln(cmd.OutOrStdout(), "windows launch uses the Linux runtime inside WSL2 Ubuntu")
	}
	if err := os.MkdirAll(config.ConfigDir(), 0o700); err != nil {
		return fmt.Errorf("create AGX config dir: %w", err)
	}
	cfg, warnings := config.LoadGlobal()
	if len(warnings) > 0 {
		return warnings[0]
	}
	if err := requireLaunchTool("git"); err != nil {
		return err
	}
	if err := requireLaunchTool("tmux"); err != nil {
		return err
	}
	warnIfDefaultAgentMissing(cmd, cfg)
	client := agxruntime.NewClient()
	if err := ensureRuntimeLaunched(ctx, cmd, client, opts.Wait); err != nil {
		return err
	}
	if opts.SkipDiscord {
		fmt.Fprintln(cmd.OutOrStdout(), "discord: skipped")
		return nil
	}
	token, guildID, allowedUserID, err := launchDiscordInputs(opts, cfg)
	if err != nil {
		return err
	}
	status, err := client.DiscordConnect(ctx, token, guildID, allowedUserID)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "discord: connected=%t guild=%s\n", status.Connected, emptyPlaceholder(status.GuildID))
	if opts.SkipDiscordSync {
		return nil
	}
	status, err = client.DiscordSoftSync(ctx)
	if err != nil {
		return err
	}
	if status.GuildName != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "discord: synced with %s\n", status.GuildName)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "discord: synced with guild %s\n", status.GuildID)
	return nil
}

func normalizeLaunchPlatform(value, goos string) (string, error) {
	platform := strings.ToLower(strings.TrimSpace(value))
	if platform == "" || platform == "auto" {
		switch goos {
		case "darwin":
			platform = "macos"
		case "linux":
			platform = "linux"
		default:
			return "", fmt.Errorf("unsupported host platform %q; use WSL2 Ubuntu for Windows", goos)
		}
	}
	if platform == "darwin" {
		platform = "macos"
	}
	switch platform {
	case "windows":
		if goos != "linux" {
			return "", fmt.Errorf("windows launch must be run inside WSL2 Ubuntu")
		}
		return platform, nil
	case "macos":
		if goos != "darwin" {
			return "", fmt.Errorf("macos launch must be run on macOS")
		}
		return platform, nil
	case "linux":
		if goos != "linux" {
			return "", fmt.Errorf("linux launch must be run on Linux")
		}
		return platform, nil
	default:
		return "", fmt.Errorf("unsupported launch platform %q; supported values: windows, macos, linux", value)
	}
}

func launchDiscordInputs(opts launchOptions, cfg config.Config) (token, guildID, allowedUserID string, err error) {
	token = strings.TrimSpace(opts.DiscordToken)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("DISCORD_BOT_TOKEN"))
	}
	if token == "" && !(cfg.Discord.Enabled && strings.TrimSpace(cfg.Discord.BotToken) != "") {
		return "", "", "", fmt.Errorf("discord bot token is required; set DISCORD_BOT_TOKEN or pass --discord-token")
	}
	guildID = strings.TrimSpace(opts.DiscordGuildID)
	if guildID == "" {
		guildID = strings.TrimSpace(cfg.Discord.GuildID)
	}
	if guildID == "" {
		return "", "", "", fmt.Errorf("discord guild id is required; set [discord].guild_id in config.toml or pass --guild")
	}
	allowedUserID = strings.TrimSpace(opts.DiscordAllowedUserID)
	if allowedUserID == "" {
		allowedUserID = firstNonEmptyString(cfg.Discord.AllowedUserIDs)
	}
	if allowedUserID == "" {
		return "", "", "", fmt.Errorf("allowed Discord user is required; set [discord].allowed_user_ids in config.toml or pass --allow-user")
	}
	return token, guildID, allowedUserID, nil
}

func firstNonEmptyString(values []string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func requireLaunchTool(name string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s is required for AGX launch", name)
	}
	return nil
}

func warnIfDefaultAgentMissing(cmd *cobra.Command, cfg config.Config) {
	registry := agent.NewRegistry(cfg.DefaultAgent, agent.FromConfig(cfg)...)
	defaultName := registry.DefaultName()
	for _, ag := range registry.All() {
		if ag.Name != defaultName {
			continue
		}
		if _, err := exec.LookPath(ag.Command); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: default agent %s command %q was not found on PATH\n", ag.Name, ag.Command)
		}
		return
	}
}

func ensureRuntimeLaunched(ctx context.Context, cmd *cobra.Command, client *agxruntime.Client, wait time.Duration) error {
	statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	if status, err := client.Status(statusCtx); err == nil {
		cancel()
		fmt.Fprintf(cmd.OutOrStdout(), "runtime: already running pid=%d\n", status.PID)
		return nil
	}
	cancel()
	manager := agxruntime.CurrentRuntimeServiceManager()
	if manager.Name() == "unsupported" {
		return fmt.Errorf("runtime service installation is not supported on this platform")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	message, installErr := manager.Install(ctx, exe, false)
	if installErr != nil {
		if status, err := waitForRuntime(ctx, client, 2*time.Second); err == nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: service install returned an error after runtime became reachable: %v\n", installErr)
			fmt.Fprintf(cmd.OutOrStdout(), "runtime: running pid=%d\n", status.PID)
			return nil
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: runtime service start failed, falling back to detached runtime: %v\n", installErr)
		stdoutPath, stderrPath, err := startRuntimeDetached(exe)
		if err != nil {
			return fmt.Errorf("start runtime service: %w; detached fallback failed: %v", installErr, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "runtime: detached fallback started\n")
		fmt.Fprintf(cmd.OutOrStdout(), "runtime log: %s\n", stdoutPath)
		fmt.Fprintf(cmd.OutOrStdout(), "runtime error log: %s\n", stderrPath)
		status, err := waitForRuntime(ctx, client, wait)
		if err != nil {
			return fmt.Errorf("runtime service failed and detached fallback did not become ready: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "runtime: running pid=%d\n", status.PID)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "runtime: %s\n", message)
	status, err := waitForRuntime(ctx, client, wait)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "runtime: running pid=%d\n", status.PID)
	return nil
}

func startRuntimeDetached(executable string) (stdoutPath, stderrPath string, err error) {
	stdoutPath, stderrPath = agxruntime.RuntimeLogPaths()
	if err := os.MkdirAll(filepath.Dir(stdoutPath), 0o700); err != nil {
		return "", "", err
	}
	stdoutFile, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return "", "", err
	}
	defer stdoutFile.Close()
	stderrFile, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return "", "", err
	}
	defer stderrFile.Close()
	cmd := exec.Command(executable, "runtime", "start")
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return "", "", err
	}
	return stdoutPath, stderrPath, nil
}

func waitForRuntime(ctx context.Context, client *agxruntime.Client, wait time.Duration) (agxruntime.Status, error) {
	if wait <= 0 {
		wait = 20 * time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		status, err := client.Status(waitCtx)
		if err == nil {
			return status, nil
		}
		lastErr = err
		select {
		case <-waitCtx.Done():
			return agxruntime.Status{}, fmt.Errorf("runtime did not become ready within %s: %w", wait, lastErr)
		case <-ticker.C:
		}
	}
}
