# Discord Control

AGX can mirror local projects and Discord-controlled tasks into a private
Discord server. The bot is a remote control for the AGX runtime on your machine;
AGX does not provide a hosted bot or relay.

## Before You Start

You need:

- an AGX runtime that can run a local task;
- a private Discord server where you can add bots;
- permission to create an application in the
  [Discord Developer Portal](https://discord.com/developers/applications);
- the Discord user account that will be allowed to control AGX.

Use a dedicated bot for each AGX installation. Do not run the same bot token
from multiple AGX runtimes at once.

## 1. Create a Bot Application

1. Open the [Discord Developer Portal](https://discord.com/developers/applications).
2. Choose **New Application** and give it a name such as `AGX Coding`.
3. Open **Bot** in the application sidebar. Create the bot if Discord asks.
4. Use **Reset Token** or **Copy** to obtain the bot token.
5. Under **Privileged Gateway Intents**, enable **Message Content Intent**.
   AGX needs this intent to receive ordinary follow-up messages in task
   channels. Server Members Intent and Presence Intent are not required.

The bot token is a secret. It is different from the Application ID, Public Key,
and Client Secret. Never commit it or paste it into an issue.

## 2. Copy the Required IDs

In the Discord desktop or web app, open **User Settings -> Advanced** and enable
**Developer Mode**. Then copy:

- **Server ID:** right-click the icon for the private server and choose
  **Copy Server ID**.
- **User ID:** right-click your own avatar or username and choose
  **Copy User ID**.

AGX calls the Server ID a `guild_id`. The allowed User ID must identify your
human account, not the bot application. AGX rejects commands from every other
user and from every other server.

## 3. Invite the Bot

### From AGX Desktop

1. Open AGX and select the **Discord** tab.
2. Enter the bot token, Server ID, and Allowed User ID.
3. Click **Invite AGX Coding**.
4. In the browser, select the intended private server and authorize the bot.

The generated invite asks for these scopes:

- `bot`
- `applications.commands`

It asks for these bot permissions:

- Manage Channels
- View Channels
- Send Messages
- Read Message History
- Add Reactions
- Use Application Commands

AGX does not require Administrator permission.

### Without AGX Desktop

In the Developer Portal, open **OAuth2 -> URL Generator**, select the `bot` and
`applications.commands` scopes, select the permissions listed above, and open
the generated URL. If Discord asks for an installation context, choose a server
or guild install. Add the bot to the same server whose ID you copied.

## 4. Connect AGX

### Desktop

After the bot has joined the server, return to the AGX **Discord** tab:

1. Click **Connect**.
2. Confirm that the status changes to **connected** and shows the expected
   server name.
3. Wait for the initial sync status to complete. Use **Soft Sync** later if the
   mirrored channels need repair.

Connect creates or verifies `#agx-control` and automatically starts the initial
sync of registered projects and active Discord tasks.

### CLI

For macOS, Linux, or WSL2, enter the token without putting it in shell history:

```bash
printf 'Discord bot token: '
read -r -s DISCORD_BOT_TOKEN
printf '\n'
export DISCORD_BOT_TOKEN

export DISCORD_SERVER_ID='your-server-id'
export DISCORD_USER_ID='your-user-id'
```

Start the runtime without Discord, then connect the already-invited bot:

```bash
agx launch --skip-discord
agx discord connect \
  --guild "$DISCORD_SERVER_ID" \
  --allow-user "$DISCORD_USER_ID"
```

`agx launch` detects the current platform. You may pass `--platform macos`,
`--platform linux`, or `--platform windows` explicitly. Inside WSL2, use Linux.
If the runtime is already running, omit the `agx launch --skip-discord` line.
Connect starts the initial sync automatically. Run `agx discord sync` only when
you need to repair missing or stale mirrored channels after it finishes.

On native Windows PowerShell, set the secret for the current process without
placing it directly in command history:

```powershell
$secret = Read-Host "Discord bot token" -AsSecureString
$env:DISCORD_BOT_TOKEN = [Net.NetworkCredential]::new("", $secret).Password
agx launch --platform windows --skip-discord
agx discord connect `
  --guild "your-server-id" `
  --allow-user "your-user-id"
```

### Store Stable IDs in Config

Instead of passing the IDs every time, put them in the local AGX config:

```toml
# ~/.config/agx/config.toml
[discord]
guild_id = "your-server-id"
allowed_user_ids = ["your-user-id"]
```

Then connect an already-running runtime with:

```bash
agx discord connect
```

`agx discord connect` reads `DISCORD_BOT_TOKEN` when `--token` is omitted. Flag
values override the TOML values and are saved after a successful connection.
`agx chat ...` remains available as a compatibility alias for older scripts.

## 5. Verify the Connection

Check the runtime from the terminal:

```bash
agx discord status
```

The output should show:

```text
enabled: true
connected: true
guild: <your-server-id>
guild name: <your-server-name>
allowed user: <your-user-id>
```

In Discord:

1. Confirm that `#agx-control` exists.
2. Run `/heartbeat` or `/ps` in `#agx-control`.
3. Run `/task create` to start a Discord-controlled task.
4. Send a normal message in the new task channel and confirm the agent responds.

Management commands are restricted to `#agx-control`. Task-specific commands
and ordinary follow-up messages belong in the corresponding task channel.

## Troubleshooting

### The bot connects but ignores normal messages

Enable **Message Content Intent** on the application's **Bot** page, then
disconnect and reconnect AGX:

```bash
agx discord disconnect
agx discord connect
```

Disconnecting clears the saved token, so keep `DISCORD_BOT_TOKEN` set before
reconnecting.

### Unknown Guild, Missing Access, or no channels appear

- Confirm the bot is a member of the configured server.
- Confirm that the configured Server ID is for that same server.
- Check that the bot role still has Manage Channels, View Channels, Send
  Messages, Read Message History, Add Reactions, and Use Application Commands.
- Reconnect after correcting access, then run `agx discord sync` if the initial
  sync did not repair the channels.

### Slash commands do not appear

Confirm that the invite included the `applications.commands` scope. Reinvite
the bot if necessary, reconnect AGX, and run Soft Sync. Guild-scoped commands
normally appear quickly, but the Discord client may need a refresh.

### AGX says you are not allowed

Confirm that Allowed User ID is your own human Discord user ID. Do not use the
bot ID, Application ID, or a username. Also confirm that the command was sent in
the configured server.

### Another Discord bridge is already running

Stop the other AGX runtime or bridge process that is using this bot token, then
reconnect. One bot should be controlled by one AGX runtime at a time.

### Diagnose from AGX

```bash
agx discord status
agx runtime status
agx doctor
```

The runtime status output includes the runtime log paths. In Discord,
`/runtime doctor` checks and repairs recoverable runtime issues.

## Sync Behavior and Safety

- **Soft Sync** creates or repairs the AGX control channel, project categories,
  task channels, mappings, and command permissions. Use it for normal setup and
  recovery.
- **Hard Sync** deletes AGX-managed channels and their message history, clears
  their mappings, and rebuilds them from current AGX state. It does not delete
  unrelated server channels, but it is still destructive; use it only when Soft
  Sync cannot repair the layout.
- **Disconnect** disables Discord and clears the saved bot token. It keeps the
  Server ID and Allowed User ID locally so reconnecting is easier.

AGX saves the token and IDs in `~/.config/agx/config.toml` with permissions
restricted to the local user. The token is a local secret, not an AGX-hosted
credential. Prefer `DISCORD_BOT_TOKEN` over `--token` so it does not appear in
shell history or process arguments.

Attachment and screenshot handling is described in
[Discord Attachment Handling Design](DISCORD_ATTACHMENTS_DESIGN.md).

## Command Reference

| Command | Where | Purpose |
| --- | --- | --- |
| `/ps` or `/task list` | `#agx-control` | List current tasks. |
| `/project list` | `#agx-control` | List registered projects. |
| `/project create path:<path> [name] [agent]` | `#agx-control` | Register a git project path visible to the AGX runtime. |
| `/project delete project:<ref>` | `#agx-control` | Delete a project and its tasks. |
| `/task create project:<ref> title:<title> [prompt] [agent] [workspace-mode] [all-mighty]` | `#agx-control` | Create a Discord-controlled task. |
| `/task delete task:<id>` | `#agx-control` | Delete a task and its Discord channel. |
| `/task logs task:<ref> [lines]` | `#agx-control` | Show recent terminal output for a task. |
| `/status task:<id>` | `#agx-control` | Show task status. |
| `/soft-sync` | `#agx-control` | Reconcile Discord with current AGX state. |
| `/hard-sync` | `#agx-control` | Rebuild AGX-managed channels from current state. |
| `/runtime doctor` | `#agx-control` | Check and repair recoverable runtime issues. |
| `/runtime restart` | `#agx-control` | Restart the installed AGX runtime service. |
| `/dangerously-reset-everything confirm:reset` | `#agx-control` | Delete all AGX projects, tasks, and managed channels. |
| `/heartbeat` | Control or task channel | Check runtime or task health. |
| `/interrupt` | Task channel | Interrupt the current task turn. |
| `/clear` | Task channel | Clear the task's agent context. |
| `/kill` | Task channel | Delete the task and remove its Discord channel. |
| `/logs [lines]` | Task channel | Show recent AGX runtime logs for diagnosis. |
| `/help` | `#agx-control` | Show command help. |
