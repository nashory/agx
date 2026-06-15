package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const DefaultAgent = "codex"

type Config struct {
	DefaultAgent string                 `toml:"default_agent"`
	Agents       map[string]AgentConfig `toml:"agents"`
	Worktree     WorktreeConfig         `toml:"worktree"`
	Discord      DiscordConfig          `toml:"discord"`
}

type AgentConfig struct {
	Command     string            `toml:"command"`
	Args        []string          `toml:"args"`
	ResumeArgs  []string          `toml:"resume_args"`
	PrintArgs   []string          `toml:"print_args"`
	Env         map[string]string `toml:"env"`
	Description string            `toml:"description"`
}

type ProjectConfig struct {
	DefaultAgent string                 `toml:"default_agent"`
	Agents       map[string]AgentConfig `toml:"agents"`
	Worktree     WorktreeConfig         `toml:"worktree"`
}

type WorktreeConfig struct {
	Enabled    bool   `toml:"enabled"`
	BaseBranch string `toml:"base_branch"`
}

type DiscordConfig struct {
	Enabled        bool     `toml:"enabled"`
	BotToken       string   `toml:"bot_token"`
	GuildID        string   `toml:"guild_id"`
	AllowedUserIDs []string `toml:"allowed_user_ids"`
}

func Load(projectRoot string) Config {
	cfg, _ := LoadWithWarnings(projectRoot)
	return cfg
}

func LoadWithWarnings(projectRoot string) (Config, []error) {
	cfg := defaultConfig()
	var warnings []error
	if err := loadInto(globalConfigPath(), &cfg); err != nil {
		warnings = append(warnings, err)
	}
	if projectRoot != "" {
		if err := loadProjectInto(filepath.Join(projectRoot, ".agx", "config.toml"), &cfg); err != nil {
			warnings = append(warnings, err)
		}
	}
	if cfg.DefaultAgent == "" {
		cfg.DefaultAgent = DefaultAgent
	}
	return cfg, warnings
}

func defaultConfig() Config {
	return Config{
		DefaultAgent: DefaultAgent,
		Agents:       map[string]AgentConfig{},
		Worktree: WorktreeConfig{
			Enabled: true,
		},
	}
}

func ConfigDir() string {
	if dir := os.Getenv("AGX_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".agx"
	}
	return filepath.Join(home, ".config", "agx")
}

func LoadGlobal() (Config, []error) {
	cfg := defaultConfig()
	var warnings []error
	if err := loadInto(globalConfigPath(), &cfg); err != nil {
		warnings = append(warnings, err)
	}
	if cfg.DefaultAgent == "" {
		cfg.DefaultAgent = DefaultAgent
	}
	return cfg, warnings
}

func SaveDiscord(discord DiscordConfig) error {
	cfg, warnings := LoadGlobal()
	if len(warnings) > 0 {
		return warnings[0]
	}
	cfg.Discord = discord
	return saveGlobalConfig(cfg)
}

func SaveDefaultAgent(defaultAgent string) error {
	cfg, warnings := LoadGlobal()
	if len(warnings) > 0 {
		return warnings[0]
	}
	cfg.DefaultAgent = strings.TrimSpace(defaultAgent)
	return saveGlobalConfig(cfg)
}

func saveGlobalConfig(cfg Config) error {
	if cfg.DefaultAgent == "" {
		cfg.DefaultAgent = DefaultAgent
	}
	if cfg.Agents == nil {
		cfg.Agents = map[string]AgentConfig{}
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	path := globalConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod config %s: %w", path, err)
	}
	return nil
}

func globalConfigPath() string {
	return filepath.Join(ConfigDir(), "config.toml")
}

func loadInto(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	return nil
}

func loadProjectInto(path string, cfg *Config) error {
	project, hasWorktree, err := loadProjectConfigFile(path)
	if err != nil {
		return err
	}
	if project.DefaultAgent != "" {
		cfg.DefaultAgent = project.DefaultAgent
	}
	if cfg.Agents == nil {
		cfg.Agents = map[string]AgentConfig{}
	}
	for name, agent := range project.Agents {
		cfg.Agents[name] = agent
	}
	if hasWorktree {
		cfg.Worktree = project.Worktree
	}
	return nil
}

func LoadEffectiveProjectConfig(projectRoot string) (ProjectConfig, error) {
	cfg := defaultConfig()
	if err := loadInto(globalConfigPath(), &cfg); err != nil {
		return ProjectConfig{}, err
	}
	if err := loadProjectInto(filepath.Join(projectRoot, ".agx", "config.toml"), &cfg); err != nil {
		return ProjectConfig{}, err
	}
	return ProjectConfig{
		DefaultAgent: cfg.DefaultAgent,
		Agents:       cfg.Agents,
		Worktree:     cfg.Worktree,
	}, nil
}

func loadProjectConfigFile(path string) (ProjectConfig, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ProjectConfig{}, false, nil
		}
		return ProjectConfig{}, false, fmt.Errorf("read config %s: %w", path, err)
	}
	var project ProjectConfig
	if err := toml.Unmarshal(data, &project); err != nil {
		return ProjectConfig{}, false, fmt.Errorf("parse config %s: %w", path, err)
	}
	return project, hasTopLevelTable(data, "worktree"), nil
}

func hasTopLevelTable(data []byte, name string) bool {
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return false
	}
	_, ok := raw[name]
	return ok
}
