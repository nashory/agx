package discord

import (
	"net/url"
	"strings"
	"testing"
)

func TestInviteURLIncludesBotAndCommandScopes(t *testing.T) {
	raw, err := InviteURL("client-1")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "https" || parsed.Host != "discord.com" || parsed.Path != "/oauth2/authorize" {
		t.Fatalf("invite URL = %q", raw)
	}
	values := parsed.Query()
	if values.Get("client_id") != "client-1" {
		t.Fatalf("client_id = %q, want client-1", values.Get("client_id"))
	}
	scope := values.Get("scope")
	if !strings.Contains(scope, "bot") || !strings.Contains(scope, "applications.commands") {
		t.Fatalf("scope = %q, want bot and applications.commands", scope)
	}
	if values.Get("permissions") == "" || values.Get("permissions") == "0" {
		t.Fatalf("permissions = %q, want nonzero permissions", values.Get("permissions"))
	}
}
