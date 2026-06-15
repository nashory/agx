package runtime

import (
	"net/http"
	"strings"

	"github.com/nashory/agx/internal/agent"
)

func (s *Service) handleListAgents(w http.ResponseWriter, r *http.Request) {
	projectPath := strings.TrimSpace(r.URL.Query().Get("project_path"))
	if projectID := strings.TrimSpace(r.URL.Query().Get("project_id")); projectID != "" {
		project, err := s.store.GetProject(projectID)
		if err != nil {
			writeError(w, err)
			return
		}
		projectPath = project.Path
	}
	registry := agent.RegistryForProject(projectPath)
	agents := registry.All()
	out := make([]Agent, 0, len(agents))
	for _, ag := range agents {
		out = append(out, Agent{
			Name:        ag.Name,
			Command:     ag.Command,
			Description: ag.Description,
			Available:   ag.IsAvailable(),
		})
	}
	writeJSON(w, out)
}
