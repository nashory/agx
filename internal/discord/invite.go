package discord

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

const inviteScope = "bot applications.commands"

const invitePermissions = discordgo.PermissionManageChannels |
	discordgo.PermissionViewChannel |
	discordgo.PermissionSendMessages |
	discordgo.PermissionReadMessageHistory |
	discordgo.PermissionAddReactions |
	discordgo.PermissionUseApplicationCommands

func BotApplicationID(token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("discord bot token is required")
	}
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return "", err
	}
	session.Client = &http.Client{Timeout: 15 * time.Second}
	user, err := session.User("@me")
	if err != nil {
		return "", fmt.Errorf("resolve Discord bot application: %w", err)
	}
	if user == nil || strings.TrimSpace(user.ID) == "" {
		return "", fmt.Errorf("resolve Discord bot application: empty application id")
	}
	return strings.TrimSpace(user.ID), nil
}

func InviteURL(clientID string) (string, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return "", fmt.Errorf("discord client id is required")
	}
	values := url.Values{}
	values.Set("client_id", clientID)
	values.Set("permissions", strconv.FormatInt(invitePermissions, 10))
	values.Set("scope", inviteScope)
	return "https://discord.com/oauth2/authorize?" + values.Encode(), nil
}
