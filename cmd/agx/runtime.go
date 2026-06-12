package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	agxruntime "github.com/nashory/agx/internal/runtime"
	"github.com/spf13/cobra"
)

func newRuntimeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Manage the AGX runtime daemon",
	}
	cmd.AddCommand(
		newRuntimeStartCmd(),
		newRuntimeStatusCmd(),
		newRuntimeStopCmd(),
		newRuntimeResetCmd(),
		newRuntimeInstallServiceCmd(),
		newRuntimeUninstallServiceCmd(),
	)
	return cmd
}

func newRuntimeStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the AGX runtime daemon in the foreground",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			service := agxruntime.NewService(versionString())
			fmt.Fprintf(cmd.ErrOrStderr(), "Starting AGX runtime on %s\n", agxruntime.DefaultPaths().Socket)
			if err := service.Start(ctx); err != nil && ctx.Err() == nil {
				return err
			}
			return nil
		},
	}
}

func newRuntimeStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show AGX runtime status",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
			defer cancel()
			status, err := agxruntime.NewClient().Status(ctx)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "AGX runtime running\n")
			fmt.Fprintf(cmd.OutOrStdout(), "pid: %d\n", status.PID)
			fmt.Fprintf(cmd.OutOrStdout(), "version: %s\n", status.Version)
			fmt.Fprintf(cmd.OutOrStdout(), "uptime: %ds\n", status.UptimeSeconds)
			fmt.Fprintf(cmd.OutOrStdout(), "config: %s\n", status.ConfigDir)
			fmt.Fprintf(cmd.OutOrStdout(), "socket: %s\n", status.SocketPath)
			fmt.Fprintf(cmd.OutOrStdout(), "lock: %s\n", status.LockPath)
			if discord, err := agxruntime.NewClient().DiscordStatus(ctx); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "discord enabled: %t\n", discord.Enabled)
				fmt.Fprintf(cmd.OutOrStdout(), "discord connected: %t\n", discord.Connected)
				fmt.Fprintf(cmd.OutOrStdout(), "discord guild: %s\n", emptyPlaceholder(discord.GuildID))
				if discord.Error != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "discord error: %s\n", discord.Error)
				}
			}
			printRuntimeServiceStatus(cmd, agxruntime.CurrentRuntimeServiceManager().Status(ctx))
			stdoutPath, stderrPath := agxruntime.RuntimeLogPaths()
			fmt.Fprintf(cmd.OutOrStdout(), "runtime log: %s\n", stdoutPath)
			fmt.Fprintf(cmd.OutOrStdout(), "runtime error log: %s\n", stderrPath)
			return nil
		},
	}
}

func newRuntimeStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the AGX runtime daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			if err := stopRuntime(ctx); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "AGX runtime stopping")
			return nil
		},
	}
}

func stopRuntime(ctx context.Context) error {
	paths := agxruntime.DefaultPaths()
	apiCtx, apiCancel := context.WithTimeout(ctx, 3*time.Second)
	err := agxruntime.NewClient().Shutdown(apiCtx)
	apiCancel()
	if err != nil {
		return stopRuntimeLockOwner(ctx, paths.Lock, err)
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()
	if err := waitRuntimeLockReleased(waitCtx, paths.Lock); err != nil {
		return stopRuntimeLockOwner(ctx, paths.Lock, err)
	}
	return nil
}

func waitRuntimeLockReleased(ctx context.Context, lockPath string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		held, err := agxruntime.LockHeld(lockPath)
		if err != nil {
			return err
		}
		if !held {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func stopRuntimeLockOwner(ctx context.Context, lockPath string, cause error) error {
	held, err := agxruntime.LockHeld(lockPath)
	if err != nil {
		return err
	}
	if !held {
		return nil
	}
	pid, ok, raw, err := agxruntime.LockOwnerPID(lockPath)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("runtime shutdown failed and lock owner is unknown: %w", cause)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find runtime process from lock %q: %w", raw, err)
	}
	_ = process.Signal(syscall.SIGTERM)
	termCtx, termCancel := context.WithTimeout(ctx, 5*time.Second)
	defer termCancel()
	if err := waitRuntimeLockReleased(termCtx, lockPath); err == nil {
		return nil
	}
	_ = process.Signal(syscall.SIGKILL)
	killCtx, killCancel := context.WithTimeout(ctx, 5*time.Second)
	defer killCancel()
	if err := waitRuntimeLockReleased(killCtx, lockPath); err != nil {
		return fmt.Errorf("terminate runtime pid %d from lock %q: %w", pid, raw, err)
	}
	return nil
}

func newRuntimeResetCmd() *cobra.Command {
	var confirm bool
	var includeConfig bool
	cmd := &cobra.Command{
		Use:   "reset --confirm",
		Short: "Reset AGX runtime database and generated runtime state",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !confirm {
				return fmt.Errorf("reset is destructive; rerun with --confirm")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			result, err := agxruntime.ResetState(ctx, agxruntime.ResetOptions{IncludeConfig: includeConfig})
			if err != nil {
				return err
			}
			if len(result.Removed) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "AGX runtime state already clean")
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Removed AGX runtime state:")
			for _, path := range result.Removed {
				fmt.Fprintf(cmd.OutOrStdout(), "- %s\n", path)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&confirm, "confirm", false, "confirm destructive runtime reset")
	cmd.Flags().BoolVar(&includeConfig, "include-config", false, "also remove AGX global config")
	return cmd
}

func newRuntimeInstallServiceCmd() *cobra.Command {
	var noStart bool
	cmd := &cobra.Command{
		Use:   "install-service",
		Short: "Install the AGX runtime user service",
		RunE: func(cmd *cobra.Command, args []string) error {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			exe, err = filepath.EvalSymlinks(exe)
			if err != nil {
				return err
			}
			message, err := agxruntime.CurrentRuntimeServiceManager().Install(cmd.Context(), exe, noStart)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), message)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noStart, "no-start", false, "install service files without starting the service")
	return cmd
}

func newRuntimeUninstallServiceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall-service",
		Short: "Uninstall the AGX runtime user service",
		RunE: func(cmd *cobra.Command, args []string) error {
			message, err := agxruntime.CurrentRuntimeServiceManager().Uninstall(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), message)
			return nil
		},
	}
}

func isRuntimeInvocation(args []string) bool {
	return len(args) > 1 && args[1] == "runtime"
}

func executeRuntimeCommand() {
	rootCmd := &cobra.Command{
		Use:           "agx",
		Short:         "Run and manage local coding agents through the AGX runtime",
		Version:       versionString(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	rootCmd.AddCommand(newRuntimeCmd())
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(exitCodeFor(err))
	}
}
