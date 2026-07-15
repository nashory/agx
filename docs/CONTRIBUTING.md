# Contributing to AGX

Thanks for taking the time to improve AGX. This guide explains how to get a
local development environment running, where the important code lives, what to
check before opening a PR, and what kinds of contributions are especially useful.

## What to Work On

Good contributions usually fit one of these areas:

- **Desktop multi-session UX**: task creation, task detail views, project
  navigation, terminal streaming, code preview, runtime settings, or empty/error
  states.
- **Discord control**: slash commands, task-channel behavior, sync reliability,
  structured transcript rendering, permissions, or reconnect flows.
- **Agent integrations**: better defaults for existing CLIs, support for new
  local agent CLIs, resume/print argument handling, or clearer agent status.
- **Runtime reliability**: daemon startup, recovery, tmux lifecycle, project
  access repair, worktree cleanup, or SQLite migrations.
- **Documentation and release polish**: install docs, troubleshooting, examples,
  screenshots, packaging notes, or contributor tooling.

Small focused PRs are easier to review than broad rewrites. If a change touches
runtime behavior, Desktop UI, and Discord at the same time, split it unless the
pieces cannot be validated independently.

## Requirements

AGX currently publishes prebuilt release assets for macOS arm64. The CLI/runtime
is built around Unix-style local primitives and should be portable to Linux from
source, but Linux Desktop packaging is not yet a release target.

Development from source requires:

- Go 1.26+
- Node.js 18+
- npm
- `task`
- `tmux`
- `git`
- At least one supported agent CLI if you want to run end-to-end tasks:
  `claude`, `codex`, `gemini`, `agent`, `copilot`, or `opencode`

Install host tools with Homebrew:

```bash
brew install go-task/tap/go-task tmux git
```

See [DEVELOPMENT.md](DEVELOPMENT.md) for the full build, run, and test command
reference.

## Local Setup

Fork and clone the repository:

```bash
git clone https://github.com/<you>/agx.git
cd agx
```

Install frontend dependencies and build everything:

```bash
task frontend-install
task test
task build
task desktop
```

For more detailed setup, runtime, Desktop, Discord, and packaging workflows, see
[DEVELOPMENT.md](DEVELOPMENT.md).

Run the local CLI and Desktop app:

```bash
./bin/agx --version
./bin/agx runtime install-service
./bin/agx runtime status
./bin/agx-desktop
```

For local end-to-end development, this builds the CLI and Desktop app, starts
the runtime in the background, and opens Desktop:

```bash
task dev
```

The Taskfile sets `GOPATH` to `./.gopath` by default. That keeps development
self-contained on machines where the normal home-directory Go module cache is
not writable.

## Project Structure

High-level map:

```text
cmd/agx/                  CLI commands
desktop/                  Wails Desktop app entry point
desktop/frontend/         React/Vite frontend
internal/runtime/         Local daemon, HTTP API, service managers, recovery
internal/desktop/         Desktop backend bindings
internal/db/              SQLite models, store, and migrations
internal/session/         Agent session lifecycle helpers
internal/agent/           Agent registry and config loading
internal/discord/         Discord bot, commands, sync, rendering
internal/tmux/            tmux control helpers
internal/worktree/        Git project/worktree validation
docs/                     User and contributor documentation
```

The runtime daemon owns SQLite, tmux sessions, task processes, optional
worktrees, and the Discord bridge. The CLI and Desktop app both talk to that
runtime, so changes to runtime APIs often need updates in both places.

## Development Commands

Use these before opening a PR:

```bash
task fmt
task test
task build
task frontend-install
task desktop
task smoke
task smoke-desktop
```

Useful runtime commands while developing:

```bash
task runtime-bg
task runtime-status
task runtime-stop
task doctor
```

For frontend-only checks:

```bash
npm --prefix desktop/frontend run build
```

For Go tests only:

```bash
go test ./...
```

`task test` builds the frontend first because desktop packages embed generated
frontend assets. For the complete developer command reference, see
[DEVELOPMENT.md](DEVELOPMENT.md).

## Manual Testing

For changes that affect normal task execution:

```bash
./bin/agx runtime install-service
cd /path/to/a/git/repo
./bin/agx project init
./bin/agx agent list
./bin/agx run "inspect this repository and summarize the structure"
./bin/agx ps
./bin/agx logs <task-id> --lines 100
```

For Desktop changes, verify the relevant flow in `./bin/agx-desktop`:

- project registration
- project access grant
- task creation
- task detail view
- terminal streaming
- send/interrupt/stop/restart/delete actions
- runtime settings
- Discord settings, if touched

For Discord changes, test with a private Discord server and bot:

```bash
read -rsp "Discord bot token: " DISCORD_BOT_TOKEN
export DISCORD_BOT_TOKEN

./bin/agx chat connect \
  --guild "$DISCORD_SERVER_ID" \
  --allow-user "$YOUR_DISCORD_USER_ID"

./bin/agx chat sync
./bin/agx chat status
```

Then verify the relevant Discord slash commands, channel sync behavior, and
Desktop visibility for Discord-attached tasks.

## Coding Guidelines

- Keep changes focused and consistent with nearby code.
- Prefer existing package boundaries over new abstractions.
- Add tests for behavior changes, bug fixes, migrations, and runtime edge cases.
- Keep Desktop UI dense, predictable, and useful for repeated task management.
- Do not commit generated Desktop production assets under
  `desktop/frontend/dist/`; they are built locally and ignored.
- Do not commit local runtime data, task worktrees, bot tokens, or `.agx/`
  project state.
- Run `task fmt` for Go formatting.
- Keep user-facing docs and command examples in sync with actual CLI behavior.

## Pull Request Checklist

Before opening a PR:

- The change is small enough to review.
- `task fmt` has been run.
- Relevant tests pass.
- Desktop builds if frontend or desktop backend code changed.
- Documentation is updated for user-facing behavior.
- New config, database, or runtime behavior is covered by tests or a clear manual
  test note.
- The PR description explains what changed, why, and how it was verified.

Useful PR description shape:

```markdown
## Summary

- ...
- ...

## Test Plan

- [ ] task test
- [ ] task desktop
- [ ] Manual: ...
```

## Reporting Bugs

When opening an issue, include:

- AGX version or commit SHA
- OS version and architecture
- Agent CLI used, if relevant
- Whether the runtime is installed as a user service or running foreground
- Steps to reproduce
- Expected behavior
- Actual behavior
- Relevant output from `agx doctor`, `agx runtime status`, and logs

Avoid sharing bot tokens, API keys, private repository paths, or sensitive task
transcripts in public issues.

## Security and Secrets

Do not include secrets in issues, PRs, screenshots, or test fixtures. Discord bot
tokens and agent CLI credentials should stay local to your machine. If you find a
security-sensitive issue, avoid posting exploit details publicly; open a minimal
issue asking for a secure reporting path.

## License

By contributing, you agree that your contributions are licensed under the
Apache-2.0 license used by this repository.
