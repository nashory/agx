# Development Guide

This guide explains how to build, run, and test AGX from source.

## Requirements

AGX publishes prebuilt release assets for macOS arm64, but source development is
mostly a normal Unix-style Go and React workflow.

Required tools:

- Go 1.26+
- Node.js 18+
- npm
- `tmux`
- `git`

Optional tools for end-to-end testing:

- `codex`
- `claude`
- `gemini`
- `agent`
- `copilot`
- `opencode`
- a Discord test server and bot token

Install host tools on macOS:

```bash
brew install tmux git
```

Then install and authenticate at least one agent CLI outside AGX if you want to
run real tasks.

## Clone and Bootstrap

```bash
git clone https://github.com/nashory/agx.git
cd agx
```

Install frontend dependencies:

```bash
make frontend-install
```

The Makefile sets `GOPATH` to `./.gopath` by default. This keeps local builds
self-contained and avoids relying on a writable home-directory Go module cache.

## Build

Build the CLI:

```bash
make build
./bin/agx --version
```

Build the Desktop app:

```bash
make desktop
./bin/agx-desktop
```

Build both:

```bash
make app
```

Build with release metadata:

```bash
VERSION=v0.1.0-dev make build
./bin/agx --version
```

Build macOS release artifacts:

```bash
VERSION=v0.1.0-rc.1 make package-macos
```

This target uses macOS packaging tools such as `hdiutil`.

Expected output:

```text
dist/AGX-darwin-arm64.dmg
dist/agx-darwin-arm64.tar.gz
```

Build Linux CLI/runtime release artifacts:

```bash
VERSION=v0.1.0-rc.1 make package-linux
```

Expected output:

```text
dist/agx-linux-amd64.tar.gz
dist/agx-linux-arm64.tar.gz
```

Final release checksums should be generated after all platform artifacts are
present:

```bash
make release-checksums
shasum -a 256 -c dist/checksums.txt
```

Run the lightweight artifact scan before publishing release assets:

```bash
make release-scan
```

The scan fails if package contents include local AGX state, config files,
databases, private runbooks, or secret-like filenames.

Packaging scripts should not remove artifacts created by another platform
packager. They may clean their own build staging directories only.

Build the Ubuntu Docker image:

```bash
make docker-image
```

Docker builds should use an allowlist copy pattern and `.dockerignore` should
exclude local config, `.env` files, databases, and `.agx/` state. Never rely on
the Docker build context to be free of secrets.

## Run Locally

Use [../COMMANDS.md](../COMMANDS.md) as the user-facing command guide for
macOS, Windows, and Linux. The commands below are maintainer shortcuts for
POSIX-style development environments.

Start the runtime in the foreground:

```bash
make runtime-start
```

Start the runtime in the background:

```bash
make runtime-bg
```

Check or stop it:

```bash
make runtime-status
make runtime-stop
```

Run Desktop against the local runtime:

```bash
make desktop-run
```

For the legacy POSIX end-to-end development loop, build everything, start the
runtime, and open Desktop:

```bash
make dev
```

You can also install the runtime as a user service. This uses launchd on macOS
and `systemd --user` on native Linux:

```bash
make service-install
./bin/agx runtime status
```

Remove the service when done:

```bash
make service-uninstall
```

## Test

Run the default test suite:

```bash
make test
```

`make test` builds the frontend first because Desktop packages embed generated
frontend assets.

Run Go tests directly after frontend assets have been built:

```bash
go test ./...
```

Run formatting:

```bash
make fmt
```

Run smoke checks:

```bash
make smoke
make smoke-desktop
```

Run the release verification gate:

```bash
make release-verify
```

After packaging release assets, require artifact scanning:

```bash
AGX_REQUIRE_RELEASE_ARTIFACTS=1 make release-verify
```

Package-level ownership of release checks:

- `internal/db`: migrations, compatibility, and persistent state tests.
- `internal/runtime`: runtime API, lifecycle, restart, and smoke behavior.
- `internal/discord`: Discord sync, auth, command, and failure handling.
- `internal/desktop` and `desktop/frontend`: Desktop API bridge, UI build, and
  user-visible workflow behavior.
- `scripts`: packaging, checksums, and artifact safety checks.

Run the doctor:

```bash
make doctor
```

Frontend-only build:

```bash
npm --prefix desktop/frontend run build
```

Clean local build output:

```bash
make clean
```

Remove build output, frontend dependencies, and local Go cache:

```bash
make distclean
```

## Manual Task Test

Use a disposable git repository or a project where it is safe for an agent to
read and edit files.

```bash
./bin/agx runtime install-service
cd /path/to/a/git/repo
./bin/agx project init
./bin/agx agent list
./bin/agx run "inspect this repository and summarize the structure"
./bin/agx ps
./bin/agx logs <task-id> --lines 100
```

Follow up with:

```bash
./bin/agx send <task-id> "continue with the next step"
./bin/agx interrupt <task-id>
./bin/agx stop <task-id>
```

## Manual Desktop Test

For Desktop changes, verify the affected path in `./bin/agx-desktop`:

- project registration
- project access grant
- task creation
- agent selection
- task detail view
- live terminal streaming
- logs and status refresh
- send, interrupt, stop, restart, and delete actions
- runtime settings
- Discord settings, if touched

## Manual Discord Test

Use a private Discord test server and a dedicated bot token.

```bash
read -rsp "Discord bot token: " DISCORD_BOT_TOKEN
export DISCORD_BOT_TOKEN

./bin/agx chat connect \
  --guild "$DISCORD_SERVER_ID" \
  --allow-user "$YOUR_DISCORD_USER_ID"

./bin/agx chat sync
./bin/agx chat status
```

Then verify the relevant slash commands, channel sync behavior, Desktop
visibility for Discord-attached tasks, and permission boundaries.

Never commit Discord bot tokens, server IDs tied to private infrastructure, or
task transcripts containing sensitive content.

## Recommended PR Check

Before opening a PR, run the smallest set that covers your change:

```bash
make fmt
make test
make smoke
```

If Desktop or frontend code changed:

```bash
make smoke-desktop
```

If release packaging changed:

```bash
VERSION=v0.1.0-dev make package-macos
make release-checksums
shasum -a 256 -c dist/checksums.txt
```

If Discord behavior changed, include a manual test note with the commands or
slash-command flow you verified.

## Common Paths

```text
cmd/agx/                  CLI commands
desktop/                  Wails Desktop app entry point
desktop/frontend/         React/Vite frontend
internal/runtime/         daemon, HTTP API, service managers, recovery
internal/desktop/         Desktop backend bindings
internal/db/              SQLite store and migrations
internal/session/         agent session lifecycle
internal/agent/           agent registry and config loading
internal/discord/         Discord bot, commands, sync, rendering
internal/tmux/            tmux helpers
internal/worktree/        git project/worktree validation
docs/                     user and contributor documentation
```
