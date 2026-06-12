package agent

import "github.com/nashory/agx/internal/config"

// FromConfig converts config-file agent definitions into runtime Agent values.
func FromConfig(cfg config.Config) []Agent {
	agents := make([]Agent, 0, len(cfg.Agents))
	for name, agentCfg := range cfg.Agents {
		agents = append(agents, Agent{
			Name:        name,
			Command:     agentCfg.Command,
			Args:        agentCfg.Args,
			ResumeArgs:  agentCfg.ResumeArgs,
			PrintArgs:   agentCfg.PrintArgs,
			Env:         agentCfg.Env,
			Description: agentCfg.Description,
		})
	}
	return agents
}

// RegistryForProject loads global plus project config and returns the effective
// agent registry for projectPath. Config warnings are ignored here so callers can
// still fall back to built-in agents.
func RegistryForProject(projectPath string) *Registry {
	cfg, _ := config.LoadWithWarnings(projectPath)
	return NewRegistry(cfg.DefaultAgent, FromConfig(cfg)...)
}
