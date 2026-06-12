<div align="center">

# AGX (AGent eXecution platform)

**💠 Mission control for parallel coding agents on your Mac.**

Run Claude Code, Codex, Gemini, Cursor Agent, Copilot, OpenCode, or your own
agent CLI in persistent local sessions. Manage every task from a native Desktop
app, keep remote work visible in Discord, and drop to the CLI whenever you want
direct control.

[![Release](https://img.shields.io/github/v/release/nashory/agx?label=release)](https://github.com/nashory/agx/releases)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Homebrew](https://img.shields.io/badge/Homebrew-nashory%2Ftap-FBB040.svg?logo=homebrew&logoColor=white)](docs/INSTALL.md)
[![Platform](https://img.shields.io/badge/platform-macOS%20prebuilt-lightgrey.svg)](docs/INSTALL.md)
[![Linux](https://img.shields.io/badge/Linux-source%20builds-2ea44f.svg)](docs/INSTALL.md)
[![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8.svg?logo=go&logoColor=white)](go.mod)
[![Desktop](https://img.shields.io/badge/Desktop-Wails-ff4d4d.svg)](desktop/)
[![Discord](https://img.shields.io/badge/Discord-control-5865F2.svg?logo=discord&logoColor=white)](docs/DISCORD.md)
[![CLI](https://img.shields.io/badge/CLI-supported-111111.svg)](#cli)

<p>
  <a href="#quick-start">Quick Start</a> ·
  <a href="#why-agx">Why AGX</a> ·
  <a href="#desktop">Desktop</a> ·
  <a href="#discord">Discord</a> ·
  <a href="#cli">CLI</a> ·
  <a href="#docs">Docs</a> ·
  <a href="docs/CONTRIBUTING.md">Contributing</a>
</p>

<!--
Add screenshots / video here:

![AGX Desktop](...)

https://github.com/user-attachments/assets/...
-->

</div>

## Quick Start

```bash
brew tap nashory/tap
brew install --cask nashory/tap/agx
```

AGX uses the agent CLIs already installed on your machine. Sign in to at least
one of `claude`, `codex`, `gemini`, Cursor Agent's `agent`, `copilot`, or
`opencode` before starting your first task.

```bash
brew install --formula nashory/tap/agx  # optional CLI
agx runtime install-service            # optional, also available in Desktop
```

Open **AGX** from Applications, add a git project, create a task, and choose an
agent. See [docs/INSTALL.md](docs/INSTALL.md) for prerequisites, first-run
setup, and troubleshooting.

> [!NOTE]
> Current prebuilt release assets target macOS arm64. The CLI/runtime is built
> on Unix primitives and may be built from source on Linux; Linux Desktop
> packaging is not part of the first public release.

## Why AGX?

Coding agents are easy to start and hard to supervise once several are running.
AGX gives them a shared local runtime and clear control surfaces, so parallel
work stays visible instead of disappearing into terminals and chat threads.

| Surface | What you get |
| --- | --- |
| **Desktop app** | A native workspace for projects, tasks, live terminals, logs, code preview, runtime settings, and multi-session control. |
| **Discord control** | Start Discord-attached tasks, send follow-ups, inspect status, fetch logs, and stop or interrupt agents from your server. |
| **Local runtime** | SQLite state, tmux sessions, task processes, recovery, optional git worktrees, and Discord sync owned on your machine. |
| **CLI** | Script the runtime, tail logs, send messages, inspect tasks, and attach directly to tmux when you want terminal-level control. |

AGX is for people who want multiple coding agents working in parallel without
giving up local ownership, observability, or human control.

## Highlights

- **Parallel sessions that persist**: every task gets a long-lived local session
  that can keep working while you move on.
- **Desktop-first workflow**: create, inspect, message, interrupt, restart, and
  delete tasks without living inside a terminal UI.
- **Discord as a remote control plane**: expose selected projects and tasks to a
  private Discord server while the runtime still runs locally.
- **Works with your existing agents**: Claude Code, Codex, Gemini, Cursor Agent,
  Copilot, OpenCode, and custom commands.
- **Worktree-aware execution**: run tasks in the project checkout or isolate them
  in per-task git worktrees.

## Desktop

AGX Desktop is the main workspace for managing many coding-agent sessions.

Use it to:

- register and switch between local git projects
- create local or Discord-attached tasks
- choose the agent and workspace mode per task
- watch live terminal output and task status
- send follow-up prompts
- interrupt, stop, restart, or delete tasks
- browse project files and preview code
- configure runtime and Discord settings

The Desktop app talks to the same local runtime as the CLI, so task state stays
consistent across both surfaces.

## Discord

Discord integration is optional. When enabled, AGX mirrors registered projects
and Discord-attached tasks into a server.

Typical flow:

```bash
agx chat connect \
  --token "$DISCORD_BOT_TOKEN" \
  --guild "$DISCORD_SERVER_ID" \
  --allow-user "$YOUR_DISCORD_USER_ID"

agx chat sync
agx chat status
```

Only the configured Discord user ID can control AGX. See
[docs/DISCORD.md](docs/DISCORD.md) for setup details and command reference.

## CLI

The CLI is the companion surface for scripting, diagnostics, logs, service
management, and direct tmux access.

```bash
cd /path/to/your/git/repo
agx project init
agx agent list
agx run "inspect this repository and summarize the architecture"
```

Watch and steer a task:

```bash
agx ps
agx logs <task-id> --lines 200
agx send <task-id> "continue with the next step"
agx attach <task-id>
```

## How It Works

```text
                            ┌──────────────────────┐
                            │      AGX Core        │
                            │ Projects · Tasks     │
                            │ Agents · Workspaces  │
                            └──────────┬───────────┘
                                       │
                                       ▼
                            ┌──────────────────────┐
                            │   Local Runtime      │
                            │ daemon + HTTP API    │
                            │ recovery + sync      │
                            └──────────┬───────────┘
                                       │
                 ┌─────────────────────┼─────────────────────┐
                 ▼                     ▼                     ▼
        ┌────────────────┐    ┌────────────────┐    ┌────────────────┐
        │  AGX Desktop   │    │    Discord     │    │    agx CLI     │
        │ multi-session  │    │ remote control │    │ scripts + logs │
        │ management     │    │ task channels  │    │ tmux attach    │
        └────────────────┘    └────────────────┘    └────────────────┘
```

Runtime-owned local resources:

```text
~/.config/agx/          SQLite state and config
tmux -L agx             persistent agent sessions
agent CLI processes     Claude, Codex, Gemini, Cursor, Copilot, OpenCode
.agx/worktrees/         optional per-task git worktrees
Discord bridge          optional server/channel sync and command handling
```

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the runtime model, task
statuses, and workspace behavior.

## Docs

| Guide | What it covers |
| --- | --- |
| [Install](docs/INSTALL.md) | Homebrew install, first run, runtime setup, and troubleshooting. |
| [Discord](docs/DISCORD.md) | Discord setup, CLI configuration, and slash command reference. |
| [Configuration](docs/CONFIGURATION.md) | Global config, project config, custom agents, and worktrees. |
| [Architecture](docs/ARCHITECTURE.md) | Runtime model, task states, local resources, and workspace behavior. |
| [Development](docs/DEVELOPMENT.md) | Build, run, test, smoke checks, and packaging commands. |
| [Contributing](docs/CONTRIBUTING.md) | Contribution areas, coding expectations, and PR checklist. |

## Build From Source

Source builds require Go 1.26+, Node.js 18+, npm, `tmux`, and `git`.

```bash
git clone https://github.com/nashory/agx.git
cd agx

make frontend-install
make test
make build
make desktop
```

Run the local binaries or the full development loop:

```bash
./bin/agx --version
./bin/agx-desktop
make dev
```

See [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for the full developer guide.

## Contributing

Contributions are welcome. Useful areas include Desktop UX, Discord workflows,
runtime reliability, agent integrations, documentation, and release tooling.

See [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md) for the contributor guide.

## License

Apache-2.0. See [LICENSE](LICENSE).
