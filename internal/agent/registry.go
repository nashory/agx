package agent

import (
	"fmt"
	"sort"
)

// Registry resolves configured agent names against built-in defaults and
// project/global overrides.
type Registry struct {
	agents      map[string]Agent
	defaultName string
}

// NewRegistry merges built-in agents with valid custom agents. Custom agents
// replace built-ins with the same name.
func NewRegistry(defaultAgent string, customAgents ...Agent) *Registry {
	agents := map[string]Agent{}
	for _, a := range KnownAgents() {
		agents[a.Name] = a
	}
	for _, a := range customAgents {
		if a.Name == "" || a.Command == "" {
			continue
		}
		agents[a.Name] = a
	}
	if defaultAgent == "" {
		defaultAgent = "claude"
	}
	return &Registry{agents: agents, defaultName: defaultAgent}
}

// Get returns the named agent, or the registry default when name is empty.
func (r *Registry) Get(name string) (Agent, error) {
	if name == "" {
		name = r.defaultName
	}
	agent, ok := r.agents[name]
	if !ok {
		return Agent{}, fmt.Errorf("unknown agent %q", name)
	}
	return agent, nil
}

// Available returns all configured agents whose command is executable.
func (r *Registry) Available() []Agent {
	var available []Agent
	for _, agent := range r.All() {
		if agent.IsAvailable() {
			available = append(available, agent)
		}
	}
	return available
}

// All returns every configured agent sorted by name for stable UI and CLI
// output.
func (r *Registry) All() []Agent {
	names := make([]string, 0, len(r.agents))
	for name := range r.agents {
		names = append(names, name)
	}
	sort.Strings(names)
	agents := make([]Agent, 0, len(names))
	for _, name := range names {
		agents = append(agents, r.agents[name])
	}
	return agents
}

// DefaultName returns the configured default agent name.
func (r *Registry) DefaultName() string {
	return r.defaultName
}
