package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	agxruntime "github.com/nashory/agx/internal/runtime"
	"github.com/spf13/cobra"
)

var errAgentDiagnosticNotFound = errors.New("diagnostic record not found")

type agentDiagnosticRecord struct {
	ID               string `json:"id"`
	Time             string `json:"time"`
	TaskID           string `json:"task_id"`
	TurnID           string `json:"turn_id"`
	EventID          string `json:"event_id"`
	Agent            string `json:"agent"`
	StreamKind       string `json:"stream_kind"`
	ThreadID         string `json:"thread_id"`
	Cursor           string `json:"cursor"`
	DiscordChannelID string `json:"discord_channel_id"`
	Summary          string `json:"summary"`
	RawError         string `json:"raw_error"`
	Text             string `json:"text"`
}

func newDiagnosticsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diagnostics",
		Short: "Inspect local AGX diagnostic records",
	}
	cmd.AddCommand(newDiagnosticsShowCmd(), newDiagnosticsListCmd())
	return cmd
}

func newDiagnosticsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show DIAGNOSTIC_ID",
		Short: "Show a local agent error diagnostic record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			record, err := findAgentDiagnostic(args[0])
			if err != nil {
				return err
			}
			printAgentDiagnostic(cmd, record)
			return nil
		},
	}
}

func newDiagnosticsListCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent local agent error diagnostics",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			records, err := listAgentDiagnostics(limit)
			if err != nil {
				return err
			}
			for _, record := range records {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s  %s\n", record.Time, record.ID, record.TaskID, record.Summary)
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "maximum records to show")
	return cmd
}

func findAgentDiagnostic(id string) (agentDiagnosticRecord, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return agentDiagnosticRecord{}, fmt.Errorf("diagnostic id is required")
	}
	record, err := scanAgentDiagnostics(func(record agentDiagnosticRecord) (bool, error) {
		return record.ID == id, nil
	})
	if errors.Is(err, errAgentDiagnosticNotFound) {
		return agentDiagnosticRecord{}, fmt.Errorf("diagnostic record %q not found", id)
	}
	return record, err
}

func listAgentDiagnostics(limit int) ([]agentDiagnosticRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	var records []agentDiagnosticRecord
	_, err := scanAgentDiagnostics(func(record agentDiagnosticRecord) (bool, error) {
		records = append(records, record)
		return false, nil
	})
	if err != nil && !errors.Is(err, errAgentDiagnosticNotFound) {
		return nil, err
	}
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].Time > records[j].Time
	})
	if len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}

func scanAgentDiagnostics(match func(agentDiagnosticRecord) (bool, error)) (agentDiagnosticRecord, error) {
	files, err := agentDiagnosticFiles()
	if err != nil {
		return agentDiagnosticRecord{}, err
	}
	for _, path := range files {
		file, err := os.Open(path)
		if err != nil {
			return agentDiagnosticRecord{}, err
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
		for scanner.Scan() {
			var record agentDiagnosticRecord
			if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
				_ = file.Close()
				return agentDiagnosticRecord{}, fmt.Errorf("decode %s: %w", path, err)
			}
			ok, err := match(record)
			if err != nil {
				_ = file.Close()
				return agentDiagnosticRecord{}, err
			}
			if ok {
				_ = file.Close()
				return record, nil
			}
		}
		if err := scanner.Err(); err != nil {
			_ = file.Close()
			return agentDiagnosticRecord{}, fmt.Errorf("read %s: %w", path, err)
		}
		if err := file.Close(); err != nil {
			return agentDiagnosticRecord{}, err
		}
	}
	return agentDiagnosticRecord{}, errAgentDiagnosticNotFound
}

func agentDiagnosticFiles() ([]string, error) {
	dir := filepath.Join(agxruntime.DefaultPaths().ConfigDir, "logs", "agent-errors")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no agent error diagnostics found in %s", dir)
		}
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	return files, nil
}

func printAgentDiagnostic(cmd *cobra.Command, record agentDiagnosticRecord) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "id: %s\n", record.ID)
	fmt.Fprintf(out, "time: %s\n", record.Time)
	fmt.Fprintf(out, "task: %s\n", record.TaskID)
	if record.TurnID != "" {
		fmt.Fprintf(out, "turn: %s\n", record.TurnID)
	}
	if record.Agent != "" {
		fmt.Fprintf(out, "agent: %s\n", record.Agent)
	}
	if record.StreamKind != "" {
		fmt.Fprintf(out, "stream: %s\n", record.StreamKind)
	}
	if record.ThreadID != "" {
		fmt.Fprintf(out, "thread: %s\n", record.ThreadID)
	}
	if record.DiscordChannelID != "" {
		fmt.Fprintf(out, "discord_channel: %s\n", record.DiscordChannelID)
	}
	if record.Summary != "" {
		fmt.Fprintf(out, "summary: %s\n", record.Summary)
	}
	if record.RawError != "" {
		fmt.Fprintf(out, "\nraw_error:\n%s\n", record.RawError)
	}
	if record.Text != "" {
		fmt.Fprintf(out, "\ntext:\n%s\n", record.Text)
	}
}
