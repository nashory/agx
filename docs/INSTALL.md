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

That is a packaging and validation boundary, not a fundamental architecture
boundary. The CLI/runtime uses local Unix primitives such as tmux, git, Unix
sockets, SQLite, and normal child processes. Linux Desktop packaging is not part
of the first public release yet.

The `install-service` command installs a macOS launchd user service on macOS and
a `systemd --user` service on native Linux. Docker runs the runtime in the
foreground inside the container and does not require systemd.

Native Windows builds are not supported yet.

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

- `claude`
- `codex`
- `gemini`

Verify that your chosen agent is available:

```bash
which claude || true
which codex || true
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

For native Linux service management:

```bash
agx runtime install-service
systemctl --user status dev.agx.runtime.service
```

If your distribution does not run user services after logout, enable lingering
for your user:

```bash
loginctl enable-linger "$USER"
```

## First Run

Start the local AGX runtime daemon. The runtime owns the local database, task
processes, worktrees, and Discord bridge.

For normal use on macOS, install it as a user launchd service:

```bash
agx runtime install-service
agx runtime status
```

For a one-off foreground session, use:

```bash
agx runtime start
```

You can also open AGX Desktop and use Settings -> Runtime to start the daemon or
install the service. The Desktop app talks to the runtime through a local Unix
socket and does not own the runtime database.

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
2. Open AGX Desktop.
3. Go to the Discord tab.
4. Enter your bot token, server ID, and allowed Discord user ID.
5. Use `Invite AGX Coding` to open the bot invite page.
6. Connect and run a Soft Sync.

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
agx runtime install-service
```

If Discord is connected from another process, stop the old runtime or bridge
process and reconnect.
