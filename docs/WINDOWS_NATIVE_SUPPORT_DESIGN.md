# Native Windows Support Design

This document defines the plan for running AGX natively on Windows without
requiring WSL2, Docker, or a Unix compatibility layer.

The current supported Windows path is WSL2 Ubuntu. Native Windows support is a
separate platform track because the runtime depends on Unix sockets, Unix file
locks, tmux, POSIX shell behavior, and Unix process semantics.

## Problem

AGX can be useful on Windows, but the current runtime architecture assumes a
Unix-like host:

- The runtime client talks over a Unix socket.
- Runtime and Discord ownership locks are implemented only for Unix platforms.
- Task sessions are managed through tmux.
- Session capture, resizing, interrupt, input, recovery, and kill behavior all
  route through tmux operations.
- Service installation is implemented through platform-specific Unix service
  managers.
- Many task execution assumptions depend on POSIX paths and shell quoting.

WSL2 works because it provides those Unix primitives. Native Windows does not.

## Goals

- Support native Windows runtime startup from the AGX CLI.
- Allow Discord connection on native Windows without Desktop, WSL2, or Docker.
- Support Discord slash-command task create, task remove, project create, and
  project remove against a native Windows runtime.
- Preserve existing macOS, Linux, Docker, and WSL2 behavior.
- Replace direct tmux coupling with a small session backend boundary.
- Implement a Windows session backend with the minimum terminal behavior AGX
  needs, not a full tmux clone.
- Keep agent processes and their child processes cleanly killable.
- Make task logs, failures, and session state observable from AGX logs and the
  runtime API.
- Keep the existing single-owner Discord guard so one Discord server cannot be
  controlled by two AGX runtimes at the same time.

## Non-Goals

- Shipping the macOS Desktop app on Windows as the first native Windows target.
- Replacing tmux on macOS or Linux.
- Building a complete tmux-compatible multiplexer.
- Requiring native Windows users to run WSL2 or Docker.
- Guaranteeing every agent CLI has equal native Windows support in the first
  release.
- Changing the current WSL2 Windows documentation before native support is
  implemented and validated.

## Current State

The runtime is the owner of persistent state and task execution. It owns:

- SQLite state.
- Project and task lifecycle.
- tmux sessions and windows.
- agent CLI process execution.
- Git worktrees.
- Discord bridge and channel sync.
- task recovery.

Important current constraints:

- `internal/runtime/service.go` constructs a tmux controller and passes it into
  the session manager.
- `internal/session/manager.go` depends directly on tmux operations such as
  create session, create window, send keys, send input, resize pane, capture
  pane, replace pipe pane, check window existence, kill window, and read pane
  current path.
- `internal/session/recovery.go` also depends directly on tmux.
- `internal/runtime/lock_windows.go` and `internal/discord/lock_windows.go`
  currently reject native Windows locking.
- `internal/runtime/client.go` uses Unix socket transport.
- Documentation currently presents WSL2 as the Windows path.

## Design Principles

- Keep Unix behavior stable while adding Windows support behind platform
  backends.
- Start with the smallest native Windows feature set that can create, observe,
  and kill tasks reliably.
- Prefer explicit capabilities over pretending all session backends behave like
  tmux.
- Keep Discord sync above the session backend. Discord should not know whether a
  task is backed by tmux or Windows ConPTY.
- Treat process cleanup as a correctness requirement, not best effort.
- Keep local runtime transport authenticated when TCP is used.

## Target Architecture

```text
AGX CLI / Discord bridge
        |
        v
AGX runtime API
        |
        +-- runtime transport backend
        |       +-- Unix socket       macOS/Linux/WSL2/Docker
        |       +-- localhost TCP     native Windows MVP
        |
        +-- runtime lock backend
        |       +-- Unix file lock    macOS/Linux/WSL2/Docker
        |       +-- Windows lock      native Windows
        |
        +-- session backend
        |       +-- tmux              macOS/Linux/WSL2/Docker
        |       +-- ConPTY/process    native Windows
        |
        +-- service backend
                +-- launchd           macOS
                +-- systemd user      Linux
                +-- foreground        Docker/WSL2/manual
                +-- Windows service   later native Windows phase
```

## Session Backend Boundary

tmux should become one implementation of a runtime session backend.

The backend interface should model only the operations AGX actually needs:

```go
type Backend interface {
    Capabilities() Capabilities
    Start(ctx context.Context, spec StartSpec) (Handle, error)
    Exists(ctx context.Context, ref Ref) (bool, error)
    SendText(ctx context.Context, ref Ref, text string) error
    SendInput(ctx context.Context, ref Ref, data []byte) error
    Interrupt(ctx context.Context, ref Ref) error
    Resize(ctx context.Context, ref Ref, cols, rows int) error
    Snapshot(ctx context.Context, ref Ref, lines int) (string, error)
    Stop(ctx context.Context, ref Ref) error
    Recover(ctx context.Context, tasks []TaskRef) (RecoveryResult, error)
}
```

This is not the final API shape. The implementation should be derived from the
existing manager call sites so the first refactor is mechanical and low risk.

Required capabilities:

- start an agent in a project or worktree directory
- write prompt/input to the session
- send interrupt
- resize the interactive terminal when supported
- return recent output for Desktop/TUI/logs
- stop one task
- stop all tasks for a project
- recover session state after runtime restart

tmux-specific concepts such as socket names, tmux sessions, tmux windows, pane
capture, and pipe-pane should not leak above this boundary.

## Windows Session Backend

Native Windows should use ConPTY for interactive terminal behavior. ConPTY gives
AGX a pseudoterminal without requiring tmux.

The first Windows backend should support:

- starting an agent process under PowerShell
- connecting the process to ConPTY
- appending terminal output to a task log file
- returning snapshots from the log file
- sending stdin text
- sending Ctrl+C or an equivalent interrupt
- killing the whole process tree through a Windows Job Object
- marking sessions as recoverable or non-recoverable explicitly

The Windows backend should not try to implement tmux windows. A task session is
one process tree plus one terminal stream.

Recommended state model:

```text
task row
    session_backend = "tmux" | "conpty"
    session_handle  = backend-specific stable handle
    session_pid     = root process id when applicable
    log_path        = append-only terminal log
```

If the existing schema cannot store this cleanly, add a small migration instead
of overloading tmux session names with Windows-specific data.

## Runtime Transport

Unix sockets are not available as the primary native Windows transport.

The recommended MVP transport is localhost TCP with an authentication token:

```text
127.0.0.1:<ephemeral-or-configured-port>
Authorization: Bearer <runtime-token>
```

The token should be generated on first run, stored under the AGX config
directory, and protected with Windows user ACLs. The runtime should reject
unauthenticated requests even on localhost.

Longer term, Windows named pipes can be evaluated for a more native private IPC
model. The MVP should prefer the simpler TCP path because it works with the
existing HTTP runtime API, CLI, and future Desktop clients.

## Runtime And Discord Locks

Native Windows needs real ownership locks. It must not use no-op locks.

Required locks:

- runtime lock: prevents two local runtimes from owning the same config
- Discord bridge lock: prevents two local processes from connecting the same
  AGX config to Discord
- Discord server owner guard: prevents another AGX runtime on another machine
  from controlling the same Discord server

The Windows local locks should use Windows file locking or another OS-backed
exclusive lock. The lock file should include owner diagnostics such as host,
process id, AGX version, Discord server id when applicable, and last heartbeat.

If a stale lock is detected, AGX should report the owner details and provide a
clear recovery command rather than silently stealing ownership.

## Discord Behavior

Discord should remain runtime-owned and platform-neutral.

Native Windows support must preserve:

- `/project create`
- `/project remove`
- `/task create`
- `/task remove`
- task channel creation and deletion
- task sync retry behavior
- owner guard for a Discord server
- useful AGX logs for slash-command failures

Discord task creation should not care whether the session backend is tmux or
ConPTY. It should receive normal task lifecycle errors from the runtime API and
surface them to Discord users.

## CLI And Launcher UX

Current launcher behavior treats Windows as WSL2-oriented. Do not silently
change that meaning until native Windows support is complete.

Recommended rollout:

```text
agx launch --platform windows
    current WSL2 Windows path until native support is stable

agx launch --platform windows-native
    explicit native Windows preview path

future:
    on GOOS=windows, `--platform windows` may become native Windows after docs,
    migration notes, and support checks are updated
```

`agx launch --platform windows-native` should run a doctor-style preflight:

- config directory writable
- runtime lock available
- runtime transport token present or created
- Discord server id configured or provided
- Discord owner guard available
- git available
- selected agent CLI available
- selected shell available
- ConPTY backend available

## Process Cleanup

Native Windows task cleanup must be deliberate.

The ConPTY backend should start each task in a Windows Job Object so AGX can
terminate the whole process tree on kill/delete. This is required because agent
commands may start child processes, development servers, or nested shells.

Delete and kill flows should:

- stop the session backend first
- wait for process exit with a bounded timeout
- force-kill the job if needed
- return cleanup warnings to the runtime API
- log cleanup failures with task id, project id, process id, and log path
- only remove worktrees after process cleanup has completed or failed
  explicitly

## Path And Shell Rules

Native Windows cannot reuse POSIX quoting rules.

Required behavior:

- store canonical Windows paths for native Windows projects
- never pass Windows paths through POSIX shell quoting
- use PowerShell command construction through structured arguments where
  possible
- handle spaces in project paths
- validate Git worktree behavior on Windows paths
- keep WSL2 paths and native Windows paths separate in docs and config

## Phased Plan

### P0: Design And Guardrails

- Add this design document.
- Keep docs clear that Windows is currently WSL2 unless native preview is
  explicitly selected.
- Add explicit unsupported errors for native Windows paths that are not ready.
- Make sure launch preflight errors explain WSL2 versus native Windows clearly.

### P1: Session Backend Interface

- Introduce a session backend interface around the current tmux operations.
- Move tmux-specific logic behind a tmux backend.
- Keep macOS/Linux behavior unchanged.
- Add fake backend unit tests for manager task lifecycle behavior.
- Add regression tests for start, send input, interrupt, snapshot, kill,
  recovery, and project cleanup.

### P2: Windows Transport And Locks

- Add localhost TCP runtime transport with auth token support.
- Keep Unix socket transport as the default on Unix platforms.
- Implement Windows runtime and Discord locks.
- Add tests for token validation, stale lock diagnostics, and duplicate owner
  rejection.
- Add `agx doctor` or launch preflight checks for native Windows.

### P3: ConPTY MVP

- Add the Windows session backend using ConPTY.
- Start tasks under PowerShell with a controlled environment.
- Capture terminal output to append-only logs.
- Implement snapshots from logs.
- Implement input, interrupt, and process-tree kill.
- Add Windows compile tests and targeted unit tests on `windows-latest`.

### P4: Native Discord Task Flow

- Enable Discord connect against the native Windows runtime.
- Validate project and task slash commands through fake Discord tests.
- Preserve server owner guard behavior across machines.
- Return visible Discord errors when task creation or cleanup fails.
- Verify task channel create/delete sync latency remains within the existing
  target under normal Discord API conditions.

### P5: Service And Packaging

- Add optional Windows Service support.
- Add installer or release artifact support for the native Windows CLI/runtime.
- Document setup, config, Discord connection, logs, recovery, and uninstall.
- Keep foreground runtime mode available for debugging.

### P6: Interactive Polish

- Add native Windows attach behavior if needed by TUI or future Desktop.
- Improve resize behavior and terminal rendering compatibility.
- Add richer recovery for sessions that survive runtime restarts.
- Document unsupported agent CLIs or shells clearly.

## Testing Strategy

Required test layers:

- Unit tests with a fake session backend for task lifecycle logic.
- Existing tmux integration tests on macOS/Linux.
- Windows unit tests on GitHub Actions `windows-latest`.
- Windows compile tests for `cmd/agx` and core runtime packages.
- Windows integration tests for:
  - runtime start and status
  - authenticated TCP client calls
  - ConPTY echo command
  - long-running process snapshot
  - interrupt handling
  - process-tree kill
  - task delete cleanup warnings
- Discord tests with fake Discord clients. Real Discord API calls should not be
  required in CI.

Manual release validation:

```text
native Windows:
  agx launch --platform windows-native --discord-server-id <server>
  /project create <name>
  /task create <project> <prompt>
  send a Discord message to the task channel
  kill the task
  remove the project
  verify logs and cleanup

WSL2 Windows:
  existing documented WSL2 path still works
```

## Risks

- ConPTY behavior differs across Windows versions and terminals.
- Agent CLIs may have incomplete native Windows support.
- PowerShell quoting bugs can create hard-to-debug task startup failures.
- Process-tree cleanup is easy to get wrong without Job Objects.
- TCP transport without authentication would be a local security bug.
- Stale local locks and stale Discord owner records can block legitimate users.
- Full interactive attach may be harder than task create/log/kill.

## Acceptance Criteria

Native Windows support is ready to advertise when:

- macOS, Linux, Docker, and WSL2 tests remain green.
- Native Windows build and unit tests run in CI.
- A native Windows runtime can start without WSL2 or Docker.
- `agx launch --platform windows-native --discord-server-id <server>` can start
  the runtime and connect Discord.
- Discord slash commands can create and remove projects and tasks.
- A Discord task can receive a message, run an agent, and show logs.
- Killing or deleting a task cleans up the whole process tree.
- Worktree cleanup failures are logged and returned as warnings/errors.
- A second machine is blocked from attaching to an already-owned Discord server.
- User-facing docs clearly explain native Windows versus WSL2.
