# Discord Control

AGX can mirror local projects and selected tasks into Discord so you can start,
inspect, and control coding agents from a server.

Discord support is optional. AGX still runs locally; Discord is a remote control
surface for the same runtime used by Desktop and the CLI.

Attachment and screenshot handling is designed separately in
[Discord Attachment Handling Design](DISCORD_ATTACHMENTS_DESIGN.md).

## Setup

1. Create or choose a Discord server.
2. Create a Discord bot and copy its token.
3. Open AGX Desktop.
4. Go to the Discord tab.
5. Enter the bot token, server ID, and the single Discord user ID allowed to
   control AGX.
6. Click `Invite AGX Coding`.
7. Run `Soft Sync`.

The bot token is stored in AGX's local config with file permissions restricted
to your user.

Disconnecting disables Discord and clears the stored bot token. The server ID
and allowed user ID are kept in the same local config file for convenience, so a
future reconnect on that machine only needs a fresh token. These values are not
bundled into AGX release builds.

Do not embed a shared bot token in AGX release builds. A Discord bot token is a
secret credential for that bot account; if it is shipped in a desktop app, users
can extract it and control the bot. AGX is local-first, so each installation
should use a dedicated bot token owned by that user or organization. A shared
AGX-operated bot would require a hosted backend/relay and a different trust
model.

## CLI Setup

You can also configure Discord from the CLI. This is the recommended path when
you run AGX from Linux, Docker, or WSL2 without the macOS Desktop app.

Put stable Discord IDs in your local AGX config:

```toml
# ~/.config/agx/config.toml
[discord]
guild_id = "your-discord-server-id"
allowed_user_ids = ["your-discord-user-id"]
```

Then provide the bot token at connect time:

```bash
read -rsp "Discord bot token: " DISCORD_BOT_TOKEN
export DISCORD_BOT_TOKEN

agx discord connect
agx discord sync
agx discord status
```

You can also pass the IDs as flags. Flag values override the TOML values and
are saved locally after a successful connect:

```bash
agx discord connect \
  --guild "$DISCORD_SERVER_ID" \
  --allow-user "$YOUR_DISCORD_USER_ID"
```

`agx discord connect` reads `DISCORD_BOT_TOKEN` when `--token` is omitted.
Prefer the environment variable path so the bot token does not appear in shell
history or process arguments. `agx chat ...` remains available as a compatibility
alias for older scripts.

## Command Reference

| Command | Purpose |
| --- | --- |
| `/ps` or `/task list` | List current tasks. |
| `/project list` | List registered projects. |
| `/project create path:<path> [name] [agent]` | Register a git project path with AGX. |
| `/project delete project:<ref>` | Delete a project and its tasks. |
| `/task create project:<ref> title:<title> [prompt] [agent] [workspace-mode] [all-mighty]` | Create a Discord-controlled task. |
| `/task delete task:<id>` | Delete a task and its Discord channel. |
| `/soft-sync` | Sync AGX state and stale AGX channels in the configured AGX server. |
| `/hard-sync` | Rebuild channels in the configured AGX server from current state. |
| `/status task:<id>` | Show task status. |
| `/logs` | Show a task log snapshot. |
| `/interrupt` | Interrupt the current task channel's running turn. |
| `/clear` | Clear the current task channel's agent context. |
| `/kill` | Delete the current task and remove its Discord task channel. |
| `/heartbeat` | Check task or backend health. |

Only the configured Discord user ID can control AGX.

## Notes

- Use a private Discord server for early testing.
- Use a dedicated bot token for AGX.
- Do not commit bot tokens, server IDs tied to private infrastructure, or task
  transcripts containing sensitive content.
- If Discord is connected from another AGX runtime, stop the old runtime or
  bridge process before reconnecting.
