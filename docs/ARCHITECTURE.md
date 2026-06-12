# Architecture

AGX is a local runtime with three control surfaces: Desktop, Discord, and CLI.

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

## Runtime-Owned Resources

```text
~/.config/agx/          SQLite state and config
tmux -L agx             persistent agent sessions
agent CLI processes     Claude, Codex, Gemini, Cursor, Copilot, OpenCode
.agx/worktrees/         optional per-task git worktrees
Discord bridge          optional server/channel sync and command handling
```

The runtime daemon owns task state, process lifecycle, recovery, workspace
selection, and Discord sync. Desktop and CLI both talk to the runtime instead
of managing the database or tmux sessions directly.

## Safety Boundaries

AGX runs local coding agents with access to user projects, so runtime features
must keep the following boundaries explicit:

- Docker builds should copy only source files required for the image and must
  not send local config, `.env` files, databases, or `.agx/` state into the
  Docker build context.
- Release packagers may create platform-specific build directories, but final
  checksums should be generated from the complete artifact set in one pass.
- Desktop log streams are owned by the Desktop bridge and must be scoped to the
  UI consumer that requested them. Closing one view must not cancel another
  view that is still watching the same task.
- Read-only output surfaces must not forward terminal input. Raw input should
  only be sent from UI that is clearly interactive.
- Discord message processing must be durable and idempotent across runtime
  restarts before a prompt is delivered to an agent.

## Data Model

- **Project**: one local git repository.
- **Task**: one agent session.
- **Runtime daemon**: the long-lived local owner of sessions, state, recovery,
  and Discord sync.
- **Workspace**: either the project checkout or an isolated per-task git
  worktree.

## Task Statuses

```text
active <-> waiting -> complete
offline -> active
```

- `active`: output is changing.
- `waiting`: the agent session is alive but idle.
- `complete`: the pane has returned to a shell.
- `offline`: the task has no live tmux window.

## Workspaces

AGX can run a task in the project checkout or in a per-task git worktree.

Project checkout mode is useful when you want the agent to operate directly in
the current working tree.

Worktree mode is useful when you want multiple agents to work independently.
When enabled, AGX creates:

- worktree: `<project-root>/.agx/worktrees/task-<short-id>`
- branch: `agx/task-<short-id>`
