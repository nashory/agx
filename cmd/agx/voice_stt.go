package main

import (
	"context"
	"fmt"
	"time"

	"github.com/nashory/agx/internal/voicestt"
	"github.com/spf13/cobra"
)

func newVoiceSTTCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "voice-stt",
		Short: "Manage local voice transcription setup",
	}
	cmd.AddCommand(newVoiceSTTSetupCmd())
	return cmd
}

func newVoiceSTTSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Prepare local Whisper voice transcription",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()
			result, err := voicestt.SetupLocalWhisper(ctx)
			if err != nil {
				return err
			}
			if result.Downloaded {
				fmt.Fprintf(cmd.OutOrStdout(), "downloaded model: %s\n", result.Config.ModelPath)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "model: %s\n", result.Config.ModelPath)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "ffmpeg: %s\n", displayOptionalPath(result.Config.FFmpegPath))
			fmt.Fprintf(cmd.OutOrStdout(), "whisper: %s\n", displayOptionalPath(result.Config.WhisperPath))
			fmt.Fprintf(cmd.OutOrStdout(), "mode: %s\n", result.Config.Mode)
			for _, warning := range result.Warnings {
				fmt.Fprintf(cmd.OutOrStdout(), "warning: %s\n", warning)
			}
			return nil
		},
	}
}

func displayOptionalPath(path string) string {
	if path == "" {
		return "missing"
	}
	return path
}
