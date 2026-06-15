package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/nashory/agx/internal/config"
	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
	"github.com/nashory/agx/internal/display"
)

func (s *Service) handleDiscordStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.discordStatus())
}

func (s *Service) handleDiscordConnect(w http.ResponseWriter, r *http.Request) {
	var req discordConnectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("decode discord connect request: %w", err))
		return
	}
	cfg, warnings := config.LoadGlobal()
	if len(warnings) > 0 {
		writeError(w, warnings[0])
		return
	}
	next := mergedDiscordConnectConfig(req, cfg.Discord)
	if err := agxdiscord.ValidateConfig(next); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, err)
		return
	}
	if err := config.SaveDiscord(next); err != nil {
		writeError(w, err)
		return
	}
	s.discord.Configure(next)
	s.discord.SetStore(s.store)
	if err := s.discord.Start(r.Context(), "runtime"); err != nil {
		writeError(w, err)
		return
	}
	status := s.discord.Status()
	s.bus.Publish("discord.status", status)
	writeJSON(w, status)
}

func (s *Service) handleDiscordDisconnect(w http.ResponseWriter, r *http.Request) {
	cfg, warnings := config.LoadGlobal()
	if len(warnings) > 0 {
		writeError(w, warnings[0])
		return
	}
	cfg.Discord.Enabled = false
	cfg.Discord.BotToken = ""
	if err := config.SaveDiscord(cfg.Discord); err != nil {
		writeError(w, err)
		return
	}
	if err := s.discord.Stop(); err != nil {
		writeError(w, err)
		return
	}
	s.discord.Configure(cfg.Discord)
	status := s.discord.Status()
	s.bus.Publish("discord.status", status)
	writeJSON(w, status)
}

func (s *Service) handleDiscordSoftSync(w http.ResponseWriter, r *http.Request) {
	if err := s.ensureDiscordStarted(r.Context(), false); err != nil {
		writeError(w, err)
		return
	}
	if err := s.discord.SoftSync(r.Context()); err != nil {
		if errors.Is(err, agxdiscord.ErrSyncInProgress) {
			writeErrorStatus(w, http.StatusConflict, err)
			return
		}
		writeError(w, err)
		return
	}
	status := s.discord.Status()
	s.bus.Publish("discord.status", status)
	writeJSON(w, status)
}

func (s *Service) handleDiscordTaskSync(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(r.PathValue("id"))
	if taskID == "" {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("task id is required"))
		return
	}
	task, err := s.store.GetTask(taskID)
	if err != nil {
		writeError(w, err)
		return
	}
	if task.Interface != db.TaskInterfaceDiscord {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("task %s is not a Discord task", taskID))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), discordTaskManualSyncTimeout)
	defer cancel()
	if err := s.ensureDiscordStarted(ctx, false); err != nil {
		writeError(w, err)
		return
	}
	started := time.Now()
	if err := s.discord.SyncTaskChannel(ctx, taskID); err != nil {
		log.Printf("operation=%q task=%s elapsed_ms=%d error=%v", "discord_task_sync_manual", display.ShortID(taskID), time.Since(started).Milliseconds(), err)
		if errors.Is(err, agxdiscord.ErrSyncInProgress) {
			writeErrorStatus(w, http.StatusConflict, err)
			return
		}
		writeError(w, err)
		return
	}
	log.Printf("operation=%q task=%s elapsed_ms=%d", "discord_task_sync_manual", display.ShortID(taskID), time.Since(started).Milliseconds())
	status := s.discord.Status()
	s.bus.Publish("discord.status", status)
	s.bus.Publish("task.changed", s.taskDTO(task))
	writeJSON(w, status)
}

func (s *Service) handleDiscordHardSync(w http.ResponseWriter, r *http.Request) {
	if err := s.startDiscordHardSync(""); err != nil {
		writeError(w, err)
		return
	}
	status := s.discordStatus()
	s.bus.Publish("discord.status", status)
	writeJSON(w, status)
}

func (s *Service) ensureDiscordStarted(ctx context.Context, initialSync bool) error {
	if s.discord.Status().Connected {
		return nil
	}
	cfg, _ := config.LoadGlobal()
	s.discord.Configure(cfg.Discord)
	s.discord.SetStore(s.store)
	if initialSync {
		return s.discord.Start(ctx, "runtime")
	}
	return s.discord.StartWithoutInitialSync(ctx, "runtime")
}

func (s *Service) handleDiscordInviteURL(w http.ResponseWriter, r *http.Request) {
	var req discordInviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("decode discord invite request: %w", err))
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		cfg, _ := config.LoadGlobal()
		if cfg.Discord.Enabled {
			token = cfg.Discord.BotToken
		}
	}
	clientID, err := agxdiscord.BotApplicationID(token)
	if err != nil {
		writeError(w, err)
		return
	}
	url, err := agxdiscord.InviteURL(clientID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, discordInviteResponse{URL: url})
}

func mergedDiscordConnectConfig(req discordConnectRequest, current config.DiscordConfig) config.DiscordConfig {
	token := strings.TrimSpace(req.Token)
	if token == "" && current.Enabled {
		token = strings.TrimSpace(current.BotToken)
	}
	guildID := strings.TrimSpace(req.GuildID)
	if guildID == "" {
		guildID = strings.TrimSpace(current.GuildID)
	}
	allowedUserID := strings.TrimSpace(req.AllowedUserID)
	allowedUsers := current.AllowedUserIDs
	if allowedUserID != "" {
		allowedUsers = []string{allowedUserID}
	}
	return config.DiscordConfig{
		Enabled:        true,
		BotToken:       token,
		GuildID:        guildID,
		AllowedUserIDs: cleanDiscordAllowedUsers(allowedUsers),
	}
}

func cleanDiscordAllowedUsers(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
