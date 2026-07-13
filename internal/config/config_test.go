package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadMergesGlobalAndProjectAgents(t *testing.T) {
	configDir := t.TempDir()
	projectRoot := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)

	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(`
default_agent = "global-agent"

[agents.global-agent]
command = "global-cli"
args = ["--auto"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	projectConfigDir := filepath.Join(projectRoot, ".agx")
	if err := os.MkdirAll(projectConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectConfigDir, "config.toml"), []byte(`
default_agent = "project-agent"

[agents.project-agent]
command = "project-cli"
args = ["--project-auto"]

[agents.global-agent]
command = "override-cli"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Load(projectRoot)
	if cfg.DefaultAgent != "project-agent" {
		t.Fatalf("DefaultAgent = %q, want project-agent", cfg.DefaultAgent)
	}
	if got := cfg.Agents["project-agent"].Command; got != "project-cli" {
		t.Fatalf("project agent command = %q, want project-cli", got)
	}
	if got := cfg.Agents["global-agent"].Command; got != "override-cli" {
		t.Fatalf("global agent override command = %q, want override-cli", got)
	}
}

func TestSaveDiscordPreservesGlobalConfig(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)

	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(`
default_agent = "codex"

[agents.custom]
command = "custom-cli"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	discord := DiscordConfig{
		Enabled:        true,
		BotToken:       "token",
		GuildID:        "guild",
		AllowedUserIDs: []string{"user"},
	}
	if err := SaveDiscord(discord); err != nil {
		t.Fatal(err)
	}

	cfg, warnings := LoadGlobal()
	if len(warnings) > 0 {
		t.Fatalf("LoadGlobal warnings = %v", warnings)
	}
	if cfg.DefaultAgent != "codex" {
		t.Fatalf("DefaultAgent = %q, want codex", cfg.DefaultAgent)
	}
	if cfg.Agents["custom"].Command != "custom-cli" {
		t.Fatalf("custom command = %q, want custom-cli", cfg.Agents["custom"].Command)
	}
	if cfg.Discord.GuildID != "guild" || !cfg.Discord.Enabled || len(cfg.Discord.AllowedUserIDs) != 1 {
		t.Fatalf("Discord = %#v, want saved config", cfg.Discord)
	}
	info, err := os.Stat(filepath.Join(configDir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		got := info.Mode().Perm()
		t.Fatalf("config mode = %o, want 600", got)
	}
}

func TestSaveDefaultAgentPreservesGlobalConfig(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)

	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(`
[agents.custom]
command = "custom-cli"

[discord]
enabled = true
bot_token = "token"
guild_id = "guild"
allowed_user_ids = ["user"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SaveDefaultAgent("custom"); err != nil {
		t.Fatal(err)
	}

	cfg, warnings := LoadGlobal()
	if len(warnings) > 0 {
		t.Fatalf("LoadGlobal warnings = %v", warnings)
	}
	if cfg.DefaultAgent != "custom" {
		t.Fatalf("DefaultAgent = %q, want custom", cfg.DefaultAgent)
	}
	if cfg.Agents["custom"].Command != "custom-cli" {
		t.Fatalf("custom command = %q, want custom-cli", cfg.Agents["custom"].Command)
	}
	if cfg.Discord.GuildID != "guild" || !cfg.Discord.Enabled || cfg.Discord.BotToken != "token" {
		t.Fatalf("Discord = %#v, want preserved config", cfg.Discord)
	}
	info, err := os.Stat(filepath.Join(configDir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		got := info.Mode().Perm()
		t.Fatalf("config mode = %o, want 600", got)
	}
}

func TestSaveVoiceSTTPreservesGlobalConfig(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)

	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(`
default_agent = "custom"

[agents.custom]
command = "custom-cli"

[discord]
enabled = true
bot_token = "token"
guild_id = "guild"
allowed_user_ids = ["user"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SaveVoiceSTT(VoiceSTTConfig{
		Mode:        "enabled",
		FFmpegPath:  " /opt/bin/ffmpeg ",
		WhisperPath: " /opt/bin/whisper-cli ",
		ModelPath:   " /models/ggml-base.bin ",
		Language:    " ko ",
		Timeout:     "90s",
	}); err != nil {
		t.Fatal(err)
	}

	cfg, warnings := LoadGlobal()
	if len(warnings) > 0 {
		t.Fatalf("LoadGlobal warnings = %v", warnings)
	}
	if cfg.DefaultAgent != "custom" || cfg.Agents["custom"].Command != "custom-cli" {
		t.Fatalf("global config was not preserved: %#v", cfg)
	}
	if !cfg.Discord.Enabled || cfg.Discord.BotToken != "token" || cfg.Discord.GuildID != "guild" {
		t.Fatalf("Discord = %#v, want preserved connection config", cfg.Discord)
	}
	voice := cfg.Discord.VoiceSTT
	if voice.Mode != VoiceSTTEnabled || voice.FFmpegPath != "/opt/bin/ffmpeg" || voice.WhisperPath != "/opt/bin/whisper-cli" || voice.ModelPath != "/models/ggml-base.bin" || voice.Language != "ko" || voice.Timeout != "90s" {
		t.Fatalf("VoiceSTT = %#v, want normalized enabled config", voice)
	}
}

func TestVoiceSTTDefaultsToAuto(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)

	cfg, warnings := LoadGlobal()
	if len(warnings) > 0 {
		t.Fatalf("LoadGlobal warnings = %v", warnings)
	}
	if cfg.Discord.VoiceSTT.Mode != VoiceSTTAuto || cfg.Discord.VoiceSTT.Language != "auto" || cfg.Discord.VoiceSTT.Timeout != "60s" {
		t.Fatalf("VoiceSTT defaults = %#v, want auto language and timeout", cfg.Discord.VoiceSTT)
	}
}

func TestLoadPreservesGlobalWorktreeWhenProjectOmitsWorktree(t *testing.T) {
	configDir := t.TempDir()
	projectRoot := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)

	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(`
[worktree]
enabled = true
base_branch = "main"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	projectConfigDir := filepath.Join(projectRoot, ".agx")
	if err := os.MkdirAll(projectConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectConfigDir, "config.toml"), []byte(`
default_agent = "codex"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Load(projectRoot)
	if !cfg.Worktree.Enabled {
		t.Fatal("Worktree.Enabled = false, want true from global config")
	}
	if cfg.Worktree.BaseBranch != "main" {
		t.Fatalf("Worktree.BaseBranch = %q, want main", cfg.Worktree.BaseBranch)
	}
}

func TestLoadProjectWorktreeOverridesGlobalWorktree(t *testing.T) {
	configDir := t.TempDir()
	projectRoot := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)

	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(`
[worktree]
enabled = true
base_branch = "main"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	projectConfigDir := filepath.Join(projectRoot, ".agx")
	if err := os.MkdirAll(projectConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectConfigDir, "config.toml"), []byte(`
[worktree]
enabled = false
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Load(projectRoot)
	if cfg.Worktree.Enabled {
		t.Fatal("Worktree.Enabled = true, want project override false")
	}
	if cfg.Worktree.BaseBranch != "" {
		t.Fatalf("Worktree.BaseBranch = %q, want empty project override", cfg.Worktree.BaseBranch)
	}
}

func TestLoadWithWarningsReportsInvalidTOML(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)

	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(`
default_agent = [
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, warnings := LoadWithWarnings("")
	if cfg.DefaultAgent != DefaultAgent {
		t.Fatalf("DefaultAgent = %q, want %q", cfg.DefaultAgent, DefaultAgent)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(warnings))
	}
}
