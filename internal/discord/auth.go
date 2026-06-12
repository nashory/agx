package discord

import (
	"strings"

	"github.com/nashory/agx/internal/config"
)

func IsAuthorized(cfg config.DiscordConfig, userID string) bool {
	if userID == "" || len(cfg.AllowedUserIDs) == 0 {
		return false
	}
	for _, allowed := range cfg.AllowedUserIDs {
		if strings.TrimSpace(allowed) == userID {
			return true
		}
	}
	return false
}
