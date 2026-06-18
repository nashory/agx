package runtime

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/nashory/agx/internal/agent"
	"github.com/nashory/agx/internal/config"
)

func (s *Service) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg, warnings := config.LoadGlobal()
	if len(warnings) > 0 {
		writeError(w, warnings[0])
		return
	}
	writeJSON(w, RuntimeConfig{DefaultAgent: cfg.DefaultAgent})
}

func (s *Service) handlePatchConfig(w http.ResponseWriter, r *http.Request) {
	var req patchConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("decode config request: %w", err))
		return
	}
	cfg, warnings := config.LoadGlobal()
	if len(warnings) > 0 {
		writeError(w, warnings[0])
		return
	}
	if req.DefaultAgent != nil {
		agentName := strings.TrimSpace(*req.DefaultAgent)
		if agentName == "" {
			agentName = config.DefaultAgent
		}
		if _, err := agent.RegistryForProject("").Get(agentName); err != nil {
			writeErrorStatus(w, http.StatusBadRequest, err)
			return
		}
		cfg.DefaultAgent = agentName
		if err := config.SaveDefaultAgent(agentName); err != nil {
			writeError(w, err)
			return
		}
		s.bus.Publish("config.changed", RuntimeConfig{DefaultAgent: cfg.DefaultAgent})
		logRuntimeOperation("config_update", "default_agent", cfg.DefaultAgent)
	}
	writeJSON(w, RuntimeConfig{DefaultAgent: cfg.DefaultAgent})
}
