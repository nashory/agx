package discord

import (
	"testing"

	"github.com/nashory/agx/internal/config"
)

func TestIsAuthorizedRequiresAllowlist(t *testing.T) {
	if IsAuthorized(config.DiscordConfig{}, "user") {
		t.Fatal("IsAuthorized() = true without allowlist")
	}
}

func TestIsAuthorizedByUser(t *testing.T) {
	cfg := config.DiscordConfig{
		AllowedUserIDs: []string{"user-1"},
	}
	if !IsAuthorized(cfg, "user-1") {
		t.Fatal("IsAuthorized() = false for allowed user")
	}
	if IsAuthorized(cfg, "user-2") {
		t.Fatal("IsAuthorized() = true for denied user")
	}
}

func TestValidateConfigRequiresCredentialsAndAllowlist(t *testing.T) {
	if err := ValidateConfig(config.DiscordConfig{Enabled: true, BotToken: "token", GuildID: "guild"}); err == nil {
		t.Fatal("ValidateConfig() error = nil without allowlist")
	}
	if err := ValidateConfig(config.DiscordConfig{Enabled: true, BotToken: "token", GuildID: "guild", AllowedUserIDs: []string{" "}}); err == nil {
		t.Fatal("ValidateConfig() error = nil with blank allowlist")
	}
	if err := ValidateConfig(config.DiscordConfig{Enabled: true, BotToken: "token", GuildID: "guild", AllowedUserIDs: []string{"user"}}); err != nil {
		t.Fatalf("ValidateConfig() error = %v", err)
	}
}
