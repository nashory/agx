<div align="center">

# AGX

**Mission control for parallel coding agents.**

Run Claude Code, Codex, Gemini, Cursor Agent, Copilot, OpenCode, or your own
agent CLI in persistent local sessions. Start work from a native Desktop app,
keep selected tasks reachable from Discord, and fall back to the CLI or TUI
whenever terminal-level control is the better tool.

[![Release](https://img.shields.io/github/v/release/nashory/agx?label=release)](https://github.com/nashory/agx/releases)
[![CI](https://github.com/nashory/agx/actions/workflows/ci.yml/badge.svg)](https://github.com/nashory/agx/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Homebrew](https://img.shields.io/badge/Homebrew-nashory%2Ftap-FBB040.svg?logo=homebrew&logoColor=white)](docs/INSTALL.md)
[![macOS](https://img.shields.io/badge/macOS-Desktop%20%2B%20CLI-lightgrey.svg)](docs/INSTALL.md)
[![Linux](https://img.shields.io/badge/Linux-CLI%20%2B%20runtime-2ea44f.svg)](docs/INSTALL.md)
[![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8.svg?logo=go&logoColor=white)](go.mod)
[![Discord](https://img.shields.io/badge/Discord-optional%20control-5865F2.svg?logo=discord&logoColor=white)](docs/DISCORD.md)

<p>
  <a href="#quick-start">Quick Start</a> ·
  <a href="#what-you-can-do">What You Can Do</a> ·
  <a href="#desktop">Desktop</a> ·
  <a href="#cli-and-tui">CLI and TUI</a> ·
  <a href="#discord">Discord</a> ·
  <a href="#docs">Docs</a>
</p>

</div>

## What AGX Is

AGX is a local control plane for coding agents. It gives every task a durable
runtime record, a persistent session, and a clear owner: your machine.

Instead of scattering agent work across terminal tabs, tmux panes, ad-hoc
worktrees, and chat threads, AGX puts the lifecycle in one place:

- register local git projects
- create tasks for different agents
- choose project checkout or isolated worktree execution
- watch live output and task status
- send follow-up prompts
- interrupt, stop, restart, or delete sessions
- expose selected tasks to a private Discord server
- script the same runtime from the CLI

AGX does not host your code or run a cloud backend. The runtime stores state in
your local config directory, starts local agent CLI processes, and uses local
Unix tools such as `tmux`, `git`, and SQLite.

## Quick Start

Install the macOS Desktop app:

```bash
brew tap nashory/tap
brew install --cask nashory/tap/agx
```

Install at least one supported agent CLI and sign in outside AGX:

```bash
which claude || true
which codex || true
which gemini || true
```

Open **AGX** from Applications, add a git project, grant project access, and
create your first task.

Install the companion CLI when you want scripting, logs, service management, or
direct tmux access:

```bash
brew install --formula nashory/tap/agx
agx doctor
agx runtime install-service
```

See [docs/INSTALL.md](docs/INSTALL.md) for macOS, Linux, Docker, first-run
setup, and troubleshooting.

## What You Can Do

### Run agents side by side

Create multiple tasks for one project and let each one keep its own session.
Use worktree mode when agents should edit independently, or project mode when
you want the agent to operate directly in the current checkout.

### Keep supervision visual

Desktop shows projects, tasks, live terminal output, task status, logs, file
trees, code preview, runtime controls, Discord settings, and split-session
views from one app.

### Keep remote control optional

Discord integration mirrors registered projects and selected tasks into your
server. You can inspect status, read logs, send follow-ups, interrupt work, and
clean up task channels without exposing every local task.

### Keep terminal control available

The CLI and TUI use the same runtime as Desktop. That means shell scripts,
terminal dashboards, tmux attach, Desktop controls, and Discord commands all
operate on the same task state.

## Why AGX

Coding agents are easy to start and hard to supervise once several are running.
AGX is built around the part that gets messy after the first prompt:

| Problem | AGX approach |
| --- | --- |
| Too many terminal tabs | One runtime tracks projects, tasks, sessions, logs, and status. |
| Agents stepping on each other | Per-task worktrees isolate parallel edits when you want them. |
| Lost context after closing a window | Sessions persist through Desktop and CLI reconnects. |
| Remote follow-up is awkward | Discord-attached tasks can be controlled from a private server. |
| CLI-only tools are hard to monitor | Desktop gives a visual task board and live terminal surfaces. |
| Desktop-only tools are hard to automate | `agx` exposes project, task, runtime, logs, and chat commands. |

## Desktop

AGX Desktop is the main workspace on macOS.

Use it to:

- add, edit, and delete git projects
- create local or Discord-attached tasks
- choose an agent per task
- choose `Worktree` or `Project` workspace mode
- start prepared quick tasks such as Coding Machine, Code Reviewer, or Planner
- watch task output and task status
- filter Desktop and Discord tasks
- open live sessions with terminal input
- browse files and preview code or Markdown
- add file paths as prompt context
- interrupt, stop, restart, split, or delete tasks
- manage runtime startup and Desktop preferences
- configure Discord from the app

Desktop talks to the same local runtime as the CLI, so task state stays
consistent across surfaces.

## CLI and TUI

The CLI is the companion surface for scripting, diagnostics, service
management, logs, and direct tmux access.

Create and inspect tasks:

```bash
cd /path/to/your/git/repo
agx project init
agx agent list
agx run "inspect this repository and summarize the architecture"
agx ps
```

Steer a running task:

```bash
agx logs <task-id> --lines 200
agx send <task-id> "continue with the next step"
agx interrupt <task-id>
agx attach <task-id>
```

Manage the runtime:

```bash
agx runtime status
agx runtime install-service
agx runtime stop
agx runtime start
```

Open the terminal dashboard:

```bash
agx tui
agx tui --once
```

Available top-level commands include:

```text
agent       inspect agent CLIs
attachment  manage persisted Discord attachments
chat        configure Discord integration
doctor      diagnose runtime prerequisites
project     manage projects
runtime     manage the runtime daemon
task        manage tasks
tui         open the terminal UI
```

## Discord

Discord integration is optional. When enabled, AGX mirrors local projects and
Discord-attached tasks into a server so you can control selected work remotely.

Typical CLI setup:

```bash
agx chat connect \
  --token "$DISCORD_BOT_TOKEN" \
  --guild "$DISCORD_SERVER_ID" \
  --allow-user "$YOUR_DISCORD_USER_ID"

agx chat sync
agx chat status
```

Only the configured Discord user ID can control AGX. Use a private server and a
dedicated bot token. The token and Discord config are stored locally under the
AGX config directory.

Common Discord commands include:

| Command | Purpose |
| --- | --- |
| `/ps` or `/task list` | List current tasks. |
| `/project list` | List registered projects. |
| `/status task:<id>` | Show task status. |
| `/logs` | Show a task log snapshot. |
| `/interrupt task:<id>` | Interrupt a running task. |
| `/stop task:<id>` | Stop a task. |
| `/restart task:<id>` | Restart a task. |
| `/delete task:<id>` | Delete a task. |
| `/soft-sync` | Reconcile Discord channels with runtime state. |
| `/hard-sync` | Rebuild Discord channels from runtime state. |

See [docs/DISCORD.md](docs/DISCORD.md) for setup details and the full command
reference.

## Supported Agents

AGX uses agent CLIs already installed and authenticated on your machine.

| Agent | Command |
| --- | --- |
| Claude Code | `claude` |
| OpenAI Codex CLI | `codex` |
| Gemini CLI | `gemini` |
| Cursor Agent | `agent` |
| GitHub Copilot CLI | `copilot` |
| OpenCode | `opencode` |
| Custom agents | configured in `~/.config/agx/config.toml` or `.agx/config.toml` |

Custom agents can define their command, arguments, resume behavior, print
behavior, environment variables, and description. See
[docs/CONFIGURATION.md](docs/CONFIGURATION.md).

## Workspace Modes

Every task runs in one of two workspace modes:

| Mode | Use it when | Behavior |
| --- | --- | --- |
| `Worktree` | You want parallel agents to edit independently. | AGX creates `.agx/worktrees/task-<id>` and a branch named `agx/task-<id>`. |
| `Project` | You want direct edits in the current checkout. | AGX runs the task in the project root. Only one active project-mode task is allowed per project. |

Worktree mode is the safer default for parallel work. Project mode is useful
for focused tasks where direct edits are expected.

## Platforms

| Platform | Support |
| --- | --- |
| macOS arm64 | Desktop app, CLI, runtime, launchd user service. |
| Linux amd64/arm64 | CLI, runtime, TUI, systemd user service, release tarballs. |
| Docker | Ubuntu runtime/TUI environment for Docker-capable hosts. |
| Windows | Not supported yet. |

Linux Desktop packaging is not part of the current release target. The runtime
is built on Unix primitives and is shared by Desktop, CLI, TUI, and Discord.

## Docker

Docker is optional. It is useful when you want an Ubuntu AGX environment or do
not have access to the macOS Desktop app.

From the repository root:

```bash
make -C docker build
make -C docker start
make -C docker shell
make -C docker tui
```

The Docker setup mounts a project directory, persists AGX state in a Docker
volume, and maps the container user to your host UID/GID so generated files stay
owned by you. See [docker/README.md](docker/README.md).

## How It Works

```text
          +------------------------------+
          |          AGX Runtime          |
          | projects, tasks, agents       |
          | SQLite, tmux, workspaces      |
          +---------------+--------------+
                          |
        +-----------------+-----------------+
        |                 |                 |
        v                 v                 v
  +-----------+     +-----------+     +-----------+
  |  Desktop  |     |  Discord  |     | CLI / TUI |
  | task board|     | remote    |     | scripts   |
  | sessions  |     | control   |     | tmux      |
  +-----------+     +-----------+     +-----------+
```

Runtime-owned local resources:

```text
~/.config/agx/          SQLite state, config, logs, attachments
tmux -L agx             persistent agent sessions
agent CLI processes     Claude, Codex, Gemini, Cursor, Copilot, OpenCode
.agx/worktrees/         optional per-task git worktrees
Discord bridge          optional server/channel sync and command handling
```

The runtime owns process lifecycle, task state, recovery, workspace selection,
and Discord sync. Desktop, CLI, TUI, and Discord commands all communicate with
that runtime instead of each maintaining separate state.

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for task states, runtime
resources, and workspace behavior.

## Local-First Boundaries

AGX is designed for local ownership:

- no hosted AGX backend is required
- projects stay on your machine
- task state lives under `~/.config/agx`
- agent commands run as local child processes
- Discord control is opt-in and limited to one configured Discord user
- worktree cleanup and task deletion are handled by the runtime

You are still running coding agents against real repositories. Review agent
changes before merging, keep secrets out of prompts and committed config, and
use worktree mode when multiple agents are editing in parallel.

## Docs

| Guide | What it covers |
| --- | --- |
| [Install](docs/INSTALL.md) | macOS, Linux, Docker, first run, runtime setup, and troubleshooting. |
| [Configuration](docs/CONFIGURATION.md) | Global config, project config, custom agents, and worktrees. |
| [Discord](docs/DISCORD.md) | Discord setup, CLI configuration, and command reference. |
| [Architecture](docs/ARCHITECTURE.md) | Runtime model, task states, local resources, and workspace behavior. |
| [Development](docs/DEVELOPMENT.md) | Build, run, test, smoke checks, packaging, and release commands. |
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

Run the local binaries:

```bash
./bin/agx --version
./bin/agx doctor
./bin/agx-desktop
```

Run the full development loop:

```bash
make dev
```

Build release artifacts:

```bash
VERSION=v0.1.0-dev make package-macos
VERSION=v0.1.0-dev make package-linux
make release-checksums
```

CI runs Go tests on macOS and Linux, builds the frontend, packages Linux
artifacts, and runs Docker smoke checks.

See [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for the full developer guide.

## Contributing

Contributions are welcome. Useful areas include Desktop UX, Discord workflows,
runtime reliability, agent integrations, Linux and Docker polish,
documentation, screenshots, and release tooling.

Small focused PRs are easiest to review. If a change touches runtime behavior,
Desktop UI, and Discord at the same time, split it unless the pieces cannot be
validated independently.

See [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md) for the contributor guide.

## License

Apache-2.0. See [LICENSE](LICENSE).
