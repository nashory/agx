package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nashory/agx/internal/config"
)

func TestBuildRunCommand(t *testing.T) {
	// All-mighty flags include the OS-specific sandbox-disable flag, so compute
	// expectations from the same helper the production code uses.
	codexWant := append([]string{"codex"}, sandboxDisableArgs()...)
	codexWant = append(codexWant, "--dangerously-bypass-approvals-and-sandbox", "quote ' and $HOME")
	claudeWant := append([]string{"missing-claude-for-test"}, sandboxDisableArgs()...)
	claudeWant = append(claudeWant, "--dangerously-skip-permissions", "implement auth")

	tests := []struct {
		name   string
		agent  Agent
		prompt string
		want   []string
	}{
		{
			name:   "public claude with prompt",
			agent:  Agent{Name: "claude", Command: "missing-claude-for-test"},
			prompt: "implement auth",
			want:   claudeWant,
		},
		{
			name:   "gemini with prompt",
			agent:  Agent{Name: "gemini", Command: "gemini"},
			prompt: "hello",
			want:   []string{"gemini", "--approval-mode", "yolo", "-i", "hello"},
		},
		{
			name:   "prompt is not shell split",
			agent:  Agent{Name: "codex", Command: "codex"},
			prompt: "quote ' and $HOME",
			want:   codexWant,
		},
		{
			name:   "custom agent with args",
			agent:  Agent{Name: "local", Command: "local-agent", Args: []string{"--auto", "--model", "fast"}},
			prompt: "implement auth",
			want:   []string{"local-agent", "--auto", "--model", "fast", "implement auth"},
		},
		{
			name:   "known agent override args",
			agent:  Agent{Name: "claude", Command: "claude", Args: []string{"--model", "opus"}},
			prompt: "implement auth",
			want:   []string{"claude", "--model", "opus", "implement auth"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.agent.BuildRunCommand(tt.prompt)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("BuildRunCommand() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestBuildRunCommandUsesWrappedClaudeAllMightyFlags(t *testing.T) {
	path := t.TempDir() + "/claude"
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec /usr/local/bin/claude_code/claude \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ag := Agent{Name: "claude", Command: path}

	if !ag.ShouldInjectInitialPrompt() {
		t.Fatal("ShouldInjectInitialPrompt() = false, want true for wrapped Claude")
	}
	runWant := append([]string{path}, sandboxDisableArgs()...)
	runWant = append(runWant, "--dangerously-skip-permissions")
	if got := ag.BuildRunCommand("implement auth"); !reflect.DeepEqual(got, runWant) {
		t.Fatalf("BuildRunCommand() = %#v, want %#v", got, runWant)
	}
	resumeWant := append([]string{path}, sandboxDisableArgs()...)
	resumeWant = append(resumeWant, "--dangerously-skip-permissions", "--continue")
	if got := ag.BuildResumeCommand(); !reflect.DeepEqual(got, resumeWant) {
		t.Fatalf("BuildResumeCommand() = %#v, want %#v", got, resumeWant)
	}
}

func TestBuildResumeAndPrintCommandForKnownAgentOverrides(t *testing.T) {
	ag := Agent{
		Name:       "claude",
		Command:    "claude",
		ResumeArgs: []string{"resume", "--custom"},
		PrintArgs:  []string{"print", "--custom"},
	}
	if got, want := ag.BuildResumeCommand(), []string{"claude", "resume", "--custom"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildResumeCommand() = %#v, want %#v", got, want)
	}
	if got, want := ag.BuildPrintCommand("hello"), []string{"claude", "print", "--custom", "hello"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildPrintCommand() = %#v, want %#v", got, want)
	}
}

func TestBuildResumeAndPrintCommandForCustomAgent(t *testing.T) {
	ag := Agent{
		Name:       "local",
		Command:    "local-agent",
		ResumeArgs: []string{"resume", "--last"},
		PrintArgs:  []string{"print", "--json"},
	}
	if got, want := ag.BuildResumeCommand(), []string{"local-agent", "resume", "--last"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildResumeCommand() = %#v, want %#v", got, want)
	}
	if got, want := ag.BuildPrintCommand("hello"), []string{"local-agent", "print", "--json", "hello"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildPrintCommand() = %#v, want %#v", got, want)
	}
}

func TestRegistryMergesCustomAgents(t *testing.T) {
	registry := NewRegistry("local", Agent{
		Name:        "local",
		Command:     "local-agent",
		Args:        []string{"--auto"},
		Description: "Local test agent",
	})
	ag, err := registry.Get("")
	if err != nil {
		t.Fatal(err)
	}
	if ag.Name != "local" || ag.Command != "local-agent" {
		t.Fatalf("default agent = %#v, want local custom agent", ag)
	}
	if _, err := registry.Get("claude"); err != nil {
		t.Fatalf("known agent missing after merge: %v", err)
	}
}

func TestRegistryListsAndFiltersAvailableAgents(t *testing.T) {
	dir := t.TempDir()
	availablePath := filepath.Join(dir, "available-agent")
	if err := os.WriteFile(availablePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	registry := NewRegistry("available", Agent{Name: "available", Command: "available-agent"}, Agent{Name: "missing", Command: "missing-agent"})

	if got, want := registry.DefaultName(), "available"; got != want {
		t.Fatalf("DefaultName() = %q, want %q", got, want)
	}
	all := registry.All()
	if len(all) < 2 {
		t.Fatalf("All() returned %d agents, want known plus custom agents", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].Name > all[i].Name {
			t.Fatalf("All() not sorted: %#v", all)
		}
	}
	available := registry.Available()
	if len(available) != 1 || available[0].Name != "available" {
		t.Fatalf("Available() = %#v, want only executable custom agent", available)
	}
	if !available[0].IsAvailable() {
		t.Fatal("IsAvailable() = false for executable on PATH")
	}
	if _, err := registry.Get("missing-name"); err == nil {
		t.Fatal("Get(unknown) error = nil, want error")
	}
}

func TestFromConfigAndRegistryForProject(t *testing.T) {
	agents := FromConfig(config.Config{
		Agents: map[string]config.AgentConfig{
			"local": {
				Command:     "local-agent",
				Args:        []string{"--run"},
				ResumeArgs:  []string{"resume"},
				PrintArgs:   []string{"print"},
				Env:         map[string]string{"KEY": "value"},
				Description: "Local agent",
			},
		},
	})
	if len(agents) != 1 || agents[0].Name != "local" || agents[0].Command != "local-agent" || agents[0].Env["KEY"] != "value" {
		t.Fatalf("FromConfig() = %#v, want local agent mapped from config", agents)
	}

	configDir := t.TempDir()
	projectRoot := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)
	if err := os.MkdirAll(filepath.Join(projectRoot, ".agx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, ".agx", "config.toml"), []byte(`
default_agent = "project-agent"

[agents.project-agent]
command = "project-agent-bin"
description = "Project agent"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := RegistryForProject(projectRoot)
	ag, err := registry.Get("")
	if err != nil {
		t.Fatal(err)
	}
	if ag.Name != "project-agent" || ag.Command != "project-agent-bin" {
		t.Fatalf("RegistryForProject default = %#v, want project agent", ag)
	}
}
