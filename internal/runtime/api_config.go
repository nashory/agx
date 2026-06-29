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
	writeJSON(w, runtimeConfigDTO(cfg))
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
	changed := false
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
		logRuntimeOperation("config_update", "default_agent", cfg.DefaultAgent)
		changed = true
	}
	if req.VoiceSTT != nil {
		cfg.Discord.VoiceSTT = config.VoiceSTTConfig{
			Mode:        req.VoiceSTT.Mode,
			FFmpegPath:  req.VoiceSTT.FFmpegPath,
			WhisperPath: req.VoiceSTT.WhisperPath,
			ModelPath:   req.VoiceSTT.ModelPath,
			Language:    req.VoiceSTT.Language,
			Timeout:     req.VoiceSTT.Timeout,
		}
		if err := config.SaveVoiceSTT(cfg.Discord.VoiceSTT); err != nil {
			writeError(w, err)
			return
		}
		logRuntimeOperation("config_update", "voice_stt_mode", cfg.Discord.VoiceSTT.Mode)
		changed = true
	}
	cfg, warnings = config.LoadGlobal()
	if len(warnings) > 0 {
		writeError(w, warnings[0])
		return
	}
	if changed {
		s.bus.Publish("config.changed", runtimeConfigDTO(cfg))
	}
	writeJSON(w, runtimeConfigDTO(cfg))
}

func runtimeConfigDTO(cfg config.Config) RuntimeConfig {
	return RuntimeConfig{
		DefaultAgent: cfg.DefaultAgent,
		VoiceSTT: VoiceSTTConfig{
			Mode:        cfg.Discord.VoiceSTT.Mode,
			FFmpegPath:  cfg.Discord.VoiceSTT.FFmpegPath,
			WhisperPath: cfg.Discord.VoiceSTT.WhisperPath,
			ModelPath:   cfg.Discord.VoiceSTT.ModelPath,
			Language:    cfg.Discord.VoiceSTT.Language,
			Timeout:     cfg.Discord.VoiceSTT.Timeout,
		},
	}
}
