# Install AGX

AGX prebuilt release assets ship as native macOS binaries and Linux CLI/runtime
tarballs. AGX uses the agent CLIs already installed and authenticated on your
machine, such as `claude`, `codex`, or `gemini`.

Docker is not required for native macOS or Linux installs. The Docker image is a
separate supported path for users who want an isolated Ubuntu runtime or who do
not have access to the macOS Desktop app.

## Platform Status

The first public release ships prebuilt assets for:

- macOS `arm64`
- Linux `amd64` and `arm64` CLI/runtime tarballs
- Windows `amd64` preview zip with native CLI/runtime and Desktop

That is a packaging and validation boundary, not a fundamental architecture
boundary. The macOS and Linux runtime paths use local Unix primitives such as
tmux, git, Unix sockets, SQLite, and normal child processes. Native Windows uses
ConPTY, authenticated localhost TCP, Windows file locks, and Windows Service
support. Linux Desktop packaging is not part of the first public release yet.

The `launch` command runs sanity checks, installs or starts the runtime service,
and connects Discord when configured. Under the hood, AGX uses a macOS launchd
user service on macOS and a `systemd --user` service on native Linux or WSL2
Ubuntu. Docker runs the runtime in the foreground inside the container and does
not require systemd.

If `systemd --user` is unavailable in WSL2, `agx launch` falls back to a
detached runtime process and prints the runtime log paths for diagnosis.

Native Windows Desktop is a preview. Closing and reopening Desktop should
reconnect to a still-running runtime, but tmux-style task recovery after runtime
restart is not supported yet.

## Prerequisites

Install the host tools AGX needs at runtime:

```bash
brew install tmux git
```

On Ubuntu or Debian:

```bash
sudo apt-get update
sudo apt-get install -y git tmux ca-certificates
```

Install and sign in to at least one supported agent CLI:

- `codex`
- `claude`
- `gemini`

Verify that your chosen agent is available:

```bash
which codex || true
which claude || true
which gemini || true
```

AGX release builds do not require you to install Go, Node.js, npm, Wails, or
Docker.

## Install With Homebrew

Tap the AGX Homebrew repo:

```bash
brew tap nashory/tap
```

Install the Desktop app:

```bash
brew install --cask nashory/tap/agx
```

Then launch AGX from Applications.

If macOS Gatekeeper blocks a preview build, use:

```bash
xattr -dr com.apple.quarantine /Applications/AGX.app
```

## Install the CLI

The Desktop app bundles the runtime helper it needs. Install the companion
shell CLI when you want scripting, logs, service management, or direct tmux
access:

```bash
brew install --formula nashory/tap/agx
```

Verify:

```bash
agx --version
agx --help
agx doctor
```

## Install the Linux CLI

Download and extract the Linux tarball for your architecture:

```bash
tar -xzf agx-linux-amd64.tar.gz
sudo install -m 0755 agx /usr/local/bin/agx
agx --version
agx doctor
```

When downloading from a release, verify `checksums.txt` before installing:

```bash
shasum -a 256 -c checksums.txt
```

For native Linux service management:

```bash
agx launch --platform linux
systemctl --user status dev.agx.runtime.service
```

If your distribution does not run user services after logout, enable lingering
for your user:

```bash
loginctl enable-linger "$USER"
```

On Windows, extract the preview zip and use the native Windows runtime:

```powershell
.\agx.exe launch --platform windows
.\agx-desktop.exe
```

Inside a WSL2 Linux shell, use the Linux runtime path:

```bash
agx launch --platform linux
```

## First Run

Start the local AGX runtime daemon. The runtime owns the local database, task
processes, worktrees, and Discord bridge.

For normal use on macOS, install it as a user launchd service:

```bash
agx launch --platform macos
agx runtime status
```

For a one-off foreground session, use:

```bash
agx runtime start
```

You can also open AGX Desktop and use Settings -> Runtime to start the daemon or
install the service. The Desktop app talks to the runtime through the local
platform transport and does not own the runtime database.

Open a git repository:

```bash
cd /path/to/your/repo
```

Register it with AGX:

```bash
agx project init
```

Check that AGX can see your agent CLIs:

```bash
agx agent list
```

Run a test task:

```bash
agx run "inspect this repository and summarize the project structure"
```

Check status:

```bash
agx runtime status
agx ps
```

Read logs:

```bash
agx logs <task-id> --lines 200
```

Send another message to a running task:

```bash
agx send <task-id> "continue with the next step"
```

Interrupt or stop a running task:

```bash
agx interrupt <task-id>
agx stop <task-id>
```

Open the Desktop app to manage projects and tasks visually.

## Optional: Discord Control

Discord is optional. To use it:

1. Create or choose a Discord server.
2. Create a Discord bot and copy its token.
3. On macOS Desktop, open the Discord tab, enter the bot token, server ID, and
   allowed Discord user ID, then connect and run a Soft Sync.
4. On Linux or WSL2, put the stable IDs in `~/.config/agx/config.toml` and
   connect from the CLI:

```toml
[discord]
guild_id = "your-discord-server-id"
allowed_user_ids = ["your-discord-user-id"]
```

```bash
read -rsp "Discord bot token: " DISCORD_BOT_TOKEN
export DISCORD_BOT_TOKEN
agx launch --platform linux \
  --discord-server-id "$DISCORD_SERVER_ID" \
  --allow-user "$YOUR_DISCORD_USER_ID"
```

The bot token is stored in AGX's local config with file permissions restricted
to your user. Disconnecting clears the stored bot token while keeping the server
ID and allowed user ID locally for convenience.

See [DISCORD.md](DISCORD.md) for Discord setup details and command reference.

## Build From Source

Source builds are for contributors and release maintainers. They require Go,
Node.js, npm, and the desktop frontend toolchain.

From a clean checkout:

```bash
make frontend-install
make test
make build
make desktop
```

Run the local binaries:

```bash
./bin/agx --version
./bin/agx-desktop
```

## Troubleshooting

If AGX cannot start a task:

- Run `agx doctor` and check the runtime service, `tmux`, `git`, and agent CLI
  lines. On macOS this reports launchd; on Linux this reports systemd.
- Confirm `tmux` is installed: `tmux -V`
- Confirm `git` is installed: `git --version`
- Confirm at least one agent CLI is on `PATH`
- Confirm the agent CLI is signed in outside AGX first

If Desktop opens but cannot find tasks, run:

```bash
agx runtime status
agx ps
```

If the runtime is not running, start it:

```bash
agx launch --platform linux
```

If Discord is connected from another process, stop the old runtime or bridge
process and reconnect.
