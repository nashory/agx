# Native Windows Support Design

This document defines the plan for running AGX natively on Windows without
requiring WSL2, Docker, or a Unix compatibility layer.

Native Windows is the Windows path for AGX. WSL2 is being dropped as a supported
Windows target: `agx launch --platform windows` runs AGX natively on Windows.
Native Windows support is a distinct platform track because the runtime depends
on Unix sockets, Unix file locks, tmux, POSIX shell behavior, and Unix process
semantics, none of which exist on native Windows.

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
- Preserve existing macOS, Linux, and Docker behavior. (Running the Linux binary
  inside a WSL2 Linux environment is just the Linux path and keeps working, but
  WSL2 is no longer a documented Windows target.)
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
  release. The first release targets `claude` and `codex` natively; other agent
  CLIs are best effort until validated on a real Windows host.
- Maintaining WSL2 as a supported Windows target. WSL2 is being retired as a
  documented Windows path; users who still run inside a WSL2 Linux environment
  are on the Linux platform path, not a Windows-specific one.
- Implementing interactive task attach on native Windows in the first release.
  Attach is deferred; the MVP supports task create, log/snapshot, and kill.

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
- Treat macOS/tmux behavior as the compatibility baseline. Native Windows work
  is not allowed to change tmux session naming, tmux target naming, Desktop
  terminal behavior, runtime Unix socket behavior, or existing recovery
  semantics unless a separate migration plan is reviewed.
- Start with the smallest native Windows feature set that can create, observe,
  and kill tasks reliably.
- Prefer explicit capabilities over pretending all session backends behave like
  tmux.
- Keep Discord sync above the session backend. Discord should not know whether a
  task is backed by tmux or Windows ConPTY.
- Treat process cleanup as a correctness requirement, not best effort.
- Keep local runtime transport authenticated when TCP is used.
- Prefer additive build-tagged files and injected interfaces over platform
  conditionals scattered through task logic.

## macOS And tmux Compatibility Contract

The first rule of native Windows work is that existing macOS/tmux support must
keep working exactly as it works today.

Protected behavior:

- `tmux -L agx` remains the Unix session engine.
- Existing tmux session names and window names remain stable.
- Existing Desktop terminal streaming and task card status behavior remains
  stable.
- Existing `agx task attach` behavior remains tmux-backed on Unix platforms.
- Existing Unix runtime socket path and permissions remain unchanged.
- Existing launchd support remains unchanged.
- Existing Linux, Docker, and WSL2 tmux behavior remains unchanged.
- Existing task recovery rules for tmux windows remain unchanged.
- Existing Discord channel sync behavior remains platform-neutral.

Implementation guardrails:

- Do not edit `internal/tmux` behavior while introducing Windows support unless
  a tmux regression test is added in the same change.
- Do not move tmux naming helpers into Windows-specific code.
- Do not make the session backend choose behavior from global `runtime.GOOS`
  checks inside business logic. Select the backend at runtime construction.
- Do not add Windows-only fields to public API responses unless Unix clients can
  ignore them safely.
- Do not make TCP transport the default on macOS, Linux, Docker, or WSL2.
- Do not make ConPTY packages part of non-Windows builds.
- Do not remove or weaken tests that use fake tmux scripts.

Every phase that touches shared task lifecycle code must prove the following
before merge:

```text
go test ./...
GOOS=windows GOARCH=amd64 go test -c ./cmd/agx
```

CI must also stay green for:

- Unit Tests on ubuntu-latest.
- Unit Tests on macos-latest.
- Linux packaging.
- Docker smoke.
- Frontend tests and build.
- Release verify.

If a shared refactor causes any tmux behavior change, stop native Windows work
and fix the tmux regression before continuing.

## Current tmux Call Surface

The initial backend interface should be derived from the operations already used
by the runtime. This avoids designing an abstract terminal system that is larger
than AGX needs.

Current direct tmux operations include:

| Area | Current operation | Backend meaning |
| --- | --- | --- |
| Startup | `HasTmux`, `HasServer` | Detect backend availability and recovery viability. |
| Project session | `HasSession`, `CreateSession`, `KillSession` | Create or stop a project-scoped session container. |
| Task start | `CreateWindow`, `WindowExists` | Start one task and verify it stayed alive. |
| Input | `SendKeys`, `SendEnter`, `SendInput`, `SendKey("C-c")` | Send prompt text, raw bytes, enter, and interrupt. |
| Terminal | `ResizeWindow`, `CapturePane`, `CapturePaneWithHistory` | Resize and snapshot recent output. |
| Logging | `ReplacePipePane`, `StopPipePane` | Mirror terminal output into task log files. |
| Cleanup | `KillWindow`, `WindowCount`, `WindowName` | Stop task windows and remove default project windows. |
| Workspace | `PaneCurrentPath` | Validate that a task is still in the expected directory. |

The tmux backend should initially be a thin adapter over these exact operations.
Only after that adapter is covered by regression tests should the Windows
backend be implemented.

## Refactor Strategy

The safe refactor sequence is:

1. Add a small `session.Backend` interface without changing behavior.
2. Implement `session.TmuxBackend` as a wrapper around the existing
   `tmux.Controller`.
3. Change `session.Manager` to depend on the interface while still constructing
   the tmux backend on all supported Unix paths.
4. Keep the public `NewManager` constructor compatible where possible. If a new
   constructor is required, add it alongside the old one first and migrate call
   sites in small commits.
5. Move recovery logic behind backend methods only after the manager lifecycle
   tests pass with the tmux backend.
6. Add a fake backend for unit tests. Use it to test manager behavior without
   real tmux.
7. Add the Windows backend behind `//go:build windows` after the tmux backend
   is stable.

The first refactor should not:

- add ConPTY
- add Windows TCP transport
- change database schema
- change Desktop UI
- change Discord behavior
- change tmux command construction

This keeps the highest-risk change, removing direct tmux coupling, reviewable
and reversible.

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

### Backend Capabilities

Backends should expose capabilities because tmux and ConPTY will not be
identical.

Example capabilities:

```go
type Capabilities struct {
    InteractiveAttach bool
    Resize            bool
    RecoverLiveTasks  bool
    StreamToFile      bool
    ProjectContainer  bool
}
```

Expected initial capability matrix:

| Capability | tmux backend | Windows ConPTY MVP |
| --- | --- | --- |
| Start task | yes | yes |
| Send text | yes | yes |
| Send raw input | yes | yes |
| Interrupt | yes | yes |
| Resize | yes | yes if ConPTY wrapper supports it |
| Snapshot recent output | yes, pane capture | yes, log tail |
| Stream logs | yes, pipe-pane | yes, append-only log writer |
| Recover live tasks | yes | limited in MVP |
| Direct attach | yes | not required in MVP |
| Project session container | yes | no, task process tree only |

Runtime and UI code should branch on capabilities, not on platform names.

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

Recommended package shape:

```text
internal/session/backend.go
    shared Backend interface and shared request/response structs

internal/session/tmux_backend.go
    tmux adapter, built on every current Unix runtime path

internal/session/conpty_backend_windows.go
    Windows ConPTY backend

internal/session/conpty_backend_stub.go
    non-Windows stub returning unsupported if accidentally selected
```

The Windows backend should be developed on a real Windows machine. GitHub
Actions can compile and run unit tests, but it should not be treated as enough
validation for interactive terminal behavior.

Manual Windows development matrix:

| Scenario | Why it matters |
| --- | --- |
| Windows Terminal + PowerShell | Primary native CLI environment. |
| Plain PowerShell host | Catches console assumptions hidden by Windows Terminal. |
| Project path with spaces | Validates quoting and working directory handling. |
| Long-running command | Validates output streaming and process lifetime. |
| Command spawning children | Validates Job Object cleanup. |
| Agent interrupted mid-run | Validates Ctrl+C or equivalent interrupt. |
| Runtime restart while task is running | Defines real recovery behavior. |
| Discord task create/delete | Validates end-to-end bridge behavior. |

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

### ConPTY Library Evaluation

Selected library: `github.com/UserExistsError/conpty`.

Rationale:

- Pure Go with no cgo, so `GOOS=windows` cross-compilation and the Windows CI
  compile checks stay simple.
- Supports resize, which satisfies the `Resize` capability.
- Exposes the underlying process handle for wait/kill.
- Small dependency graph and Windows-only, so it drops cleanly into
  `conpty_backend_windows.go` without leaking into non-Windows builds.

Critical separation of concerns:

- No ConPTY wrapper cleans up a process tree. The wrapper only manages the
  directly spawned process and its terminal I/O. Child processes (dev servers,
  nested shells) survive if only the wrapper is used.
- Process-tree termination is therefore handled by a Windows Job Object, not by
  the ConPTY library. Each task runs inside a Job Object; kill/delete calls
  `TerminateJobObject`. This keeps the "process cleanup is a correctness
  requirement" rule independent of the ConPTY library choice, so the library can
  be swapped later with low risk.

Validation still required on a real Windows host before the library is
considered final:

- resize under Windows Terminal and a plain console host
- clean stdin/stdout wiring
- behavior under GitHub Actions Windows runners
- no orphaned children after `TerminateJobObject`
- guard against resize-after-exit (resizing an already-exited pty can crash)
- interrupt path: confirm Ctrl+C via VT sequence vs `GenerateConsoleCtrlEvent`,
  accounting for win32-input-mode encoding

The selected dependency must stay isolated to Windows build-tagged files.

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

Transport compatibility rules:

- Unix builds continue using the Unix socket client by default.
- Windows native builds use authenticated localhost TCP by default.
- WSL2 continues using the Unix socket inside the WSL2 Linux environment.
- The runtime API routes stay the same across transports.
- Tests should verify auth failure, missing token, token rotation, and wrong
  port behavior.
- Logs should include the selected transport, bind address, token file path, and
  explicit warnings when a client is rejected for auth failure.

The TCP listener must bind to loopback only. It must never listen on `0.0.0.0`
or a LAN interface unless a future explicit remote-control design is approved.

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

### Breaking Stale Discord Ownership

The Discord server owner guard is cross-machine, so it needs a shared source of
truth that any AGX instance holding the bot token can read. Use Discord itself:
a hidden runtime-lock channel holds a single owner record message that the owner
edits in place. No extra infrastructure is required.

Owner record fields:

```json
{
  "host": "...",
  "pid": 1234,
  "agx_version": "...",
  "server_id": "...",
  "epoch": 7,
  "heartbeat_ts": "..."
}
```

The owner refreshes `heartbeat_ts` every N seconds. A record is stale when
`now - heartbeat_ts > k*N` (for example k=3).

Takeover flow (detection is automatic, takeover is always explicit):

1. Detect: on connect, a new instance reading a stale record must NOT auto-steal.
   It prints the old owner details and the exact recovery command.
2. Explicit command: the operator runs a takeover command such as
   `agx discord takeover --server-id <id> --confirm`. Silent steal is forbidden.
3. Fencing grace: before claiming, the new instance records a takeover intent
   with its own identity and waits one heartbeat interval. If the old owner
   refreshes its heartbeat during the wait, it is alive; abort the takeover to
   avoid split-brain.
4. Compare-and-swap claim: overwrite the owner record only if it still equals the
   stale record that was read, and increment `epoch`. This blocks two reviving
   machines from both taking over.
5. Self-fence: when a previous owner resumes and finds an `epoch` newer than its
   own, it must disconnect gracefully instead of contending for the server.
6. Audit: log the old owner metadata before replacement and post an ownership
   transfer notice in the runtime-lock/control channel.

Locking behavior must be tested separately from Discord behavior:

- two runtimes using the same config on one Windows machine
- two Discord bridges using the same config on one Windows machine
- two machines trying to own the same Discord server
- stale local lock with dead process
- stale remote owner with expired heartbeat
- explicit takeover of a stale remote owner succeeds and increments epoch
- takeover aborts when the old owner refreshes its heartbeat during the grace
  wait
- concurrent takeover from two machines: compare-and-swap lets only one win
- resumed old owner self-fences when it sees a newer epoch
- corrupt lock metadata

Any lock bypass should require an explicit command and should log the old owner
metadata before replacement.

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

Native Windows Discord support should be implemented after the runtime can
create and kill local tasks without Discord. Otherwise Discord failures will
hide lower-level session bugs.

Slash-command behavior should be tested through fake Discord clients:

| Command | Runtime dependency | Expected failure surface |
| --- | --- | --- |
| `/project create` | store, path validation | Discord ephemeral error and AGX log |
| `/project remove` | store, task cleanup | Discord ephemeral error and AGX log |
| `/task create` | session backend, worktree, store | Discord ephemeral error and AGX log |
| `/task remove` | session backend cleanup | Discord ephemeral error and AGX log |

The Discord bridge should not contain Windows-specific process logic.

## CLI And Launcher UX

`agx launch --platform windows` means native Windows. There is no separate
`windows-native` flag and no WSL2 launch path. WSL2 has been dropped as a
Windows target.

```text
agx launch --platform windows
    native Windows runtime; only valid on GOOS=windows

    (Running inside a WSL2 Linux shell is the Linux path: use
     `agx launch --platform linux` there. `--platform windows` is native only.)
```

`agx launch --platform windows` should run a doctor-style preflight:

- config directory writable
- runtime lock available
- runtime transport token present or created
- Discord server id configured or provided
- Discord owner guard available
- git available
- selected agent CLI available (first release: `claude`, `codex`)
- selected shell available
- ConPTY backend available

If `--platform windows` is attempted on macOS, Linux, or Docker (a non-Windows
GOOS), the command should fail with a clear message that native Windows requires
a real Windows host. During development, before the ConPTY backend and native
transport are stable, the preflight should report exactly which native
capability is missing rather than silently falling back to any Unix path.

Launcher preflight should print a concise table:

```text
AGX native Windows preflight
  config directory     ok
  runtime lock         ok
  runtime transport    ok, 127.0.0.1:<port>
  discord server id    ok
  discord owner guard  ok
  git                  ok
  agent: codex         missing
  shell: powershell    ok
  conpty               ok
```

Preflight should fail before starting Discord if required local execution
dependencies are missing.

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

Cleanup error policy:

| Step | If it fails |
| --- | --- |
| graceful interrupt | continue to forced stop after timeout; log warning |
| forced process kill | return error; do not pretend task cleanup succeeded |
| worktree delete | return warning/error through API and log exact path |
| database status update | return error; preserve cleanup diagnostics |
| Discord channel delete | retry asynchronously; do not block local process cleanup |

This is intentionally stricter than best effort cleanup. Hidden cleanup failures
create stale worktrees and orphaned processes, which are worse on Windows where
file locks can make later deletes confusing.

## Path And Shell Rules

Native Windows cannot reuse POSIX quoting rules.

Required behavior:

- store canonical Windows paths for native Windows projects
- never pass Windows paths through POSIX shell quoting
- use PowerShell command construction through structured arguments where
  possible
- handle spaces in project paths
- validate Git worktree behavior on Windows paths
- store canonical Windows paths only; do not accept WSL-style paths for native
  Windows projects

Shell execution should avoid constructing one large command string when
possible. Prefer:

- explicit executable path
- explicit argument array
- explicit working directory
- explicit environment
- script files for complex startup sequences

When script files are required, write PowerShell scripts with Windows line
endings and quote values through PowerShell-safe helpers, not POSIX shell
helpers.

Path compatibility tests should include:

```text
C:\Users\name\src\agx
C:\Users\name\src\project with spaces
C:\Users\name\.config\agx\worktrees\<task>
```

WSL paths such as `/mnt/c/...` are not native Windows paths and must be rejected
by native Windows project registration. WSL2 interop is out of scope.

## Database And Migration Rules

Native Windows support may require storing backend metadata. Schema changes must
be additive and backward compatible.

Rules:

- Existing rows with `session_name` continue to mean tmux-backed tasks.
- Existing Desktop builds should not crash when reading rows created before the
  migration.
- New nullable fields are preferred over changing the meaning of existing tmux
  fields.
- Migrations must be idempotent and tested from an old schema fixture.
- Downgrade behavior should be documented if a new schema cannot be read by an
  older binary.

Do not store Windows process ids in fields that currently mean tmux window
names. That makes recovery and debugging ambiguous.

## Observability

Native Windows failures must be diagnosable from AGX logs without attaching a
debugger.

Required log events:

- selected session backend and capabilities
- selected runtime transport and bind address
- runtime token file path, without printing token contents
- lock acquisition, owner metadata, and stale lock decisions
- project registration path normalization
- task start request with backend, shell, working directory, and agent name
- process start result with pid and log path
- ConPTY attach/start failure details
- input send failures
- interrupt request and result
- graceful stop timeout
- forced kill request and result
- worktree cleanup success/failure
- Discord command request id and runtime task id correlation

Logs must never include:

- Discord bot token
- runtime auth token
- agent provider API keys
- full prompt text unless the existing logging policy already records it

## Phased Plan

### P0: Design And Guardrails

- Add this design document and keep it updated as implementation discovers
  Windows-specific constraints.
- Make docs clear that `--platform windows` is native Windows and that WSL2 is no
  longer a supported Windows target.
- Add explicit unsupported errors for native Windows paths that are not ready.
- Make sure launch preflight errors name the exact missing native capability.
- Add CI compile checks for Windows if they are missing.

### P1: Session Backend Interface

- Introduce a session backend interface around the current tmux operations.
- Move tmux-specific logic behind a tmux backend.
- Keep macOS/Linux behavior unchanged.
- Add fake backend unit tests for manager task lifecycle behavior.
- Add regression tests for start, send input, interrupt, snapshot, kill,
  recovery, and project cleanup.
- Confirm Desktop terminal streaming and `agx task attach` still use the tmux
  backend on macOS.
- Commit this phase before any Windows ConPTY code is added.

### P2: Windows Transport And Locks

- Add localhost TCP runtime transport with auth token support.
- Keep Unix socket transport as the default on Unix platforms.
- Implement Windows runtime and Discord locks.
- Add tests for token validation, stale lock diagnostics, and duplicate owner
  rejection.
- Add `agx doctor` or launch preflight checks for native Windows.
- Keep Unix (macOS/Linux/Docker) launch behavior unchanged.

### P3: ConPTY MVP

- Add the Windows session backend using ConPTY.
- Start tasks under PowerShell with a controlled environment.
- Capture terminal output to append-only logs.
- Implement snapshots from logs.
- Implement input, interrupt, and process-tree kill.
- Add Windows compile tests and targeted unit tests on `windows-latest`.
- Validate manually on a real Windows machine before advertising the feature.
- Keep direct attach out of scope unless the basic task lifecycle is stable.

### P4: Native Discord Task Flow

- Enable Discord connect against the native Windows runtime.
- Validate project and task slash commands through fake Discord tests.
- Preserve server owner guard behavior across machines.
- Return visible Discord errors when task creation or cleanup fails.
- Verify task channel create/delete sync latency remains within the existing
  target under normal Discord API conditions.
- Verify a second machine is blocked from controlling an already-owned Discord
  server.
- Implement the explicit stale-ownership takeover command with epoch increment,
  fencing grace, and compare-and-swap claim. Never steal ownership silently.

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

Regression tests that protect existing tmux behavior:

- tmux command construction tests.
- manager lifecycle tests using fake tmux scripts.
- task start failure cleanup tests.
- task stop/delete cleanup warning tests.
- recovery tests for missing tmux server, missing session, and missing window.
- Desktop tests proving the runtime owns tmux rather than Desktop creating its
  own controller unexpectedly.
- CLI attach tests proving Unix attach still targets the stored tmux window.

Manual release validation:

```text
native Windows:
  agx launch --platform windows --discord-server-id <server>
  /project create <name>
  /task create <project> <prompt>
  send a Discord message to the task channel
  kill the task
  remove the project
  verify logs and cleanup

macOS regression:
  create a project from Desktop
  create a task in project mode
  stream terminal output
  send input
  interrupt
  attach through CLI
  kill/delete task
  verify worktree cleanup

Linux regression:
  agx launch --platform linux as documented
  create task through CLI or Discord
  verify tmux session exists
  kill/delete task
  verify runtime socket path remains Unix socket
```

Windows machine validation should be tracked separately from GitHub CI. The
feature should not be marked complete based only on cross-compilation.

## Risks

- ConPTY behavior differs across Windows versions and terminals.
- Agent CLIs may have incomplete native Windows support.
- PowerShell quoting bugs can create hard-to-debug task startup failures.
- Process-tree cleanup is easy to get wrong without Job Objects.
- TCP transport without authentication would be a local security bug.
- Stale local locks and stale Discord owner records can block legitimate users.
- Full interactive attach may be harder than task create/log/kill.
- Session backend abstraction may accidentally hide tmux-specific guarantees
  that Desktop depends on.
- Schema changes may make older Desktop builds read ambiguous task state.

## Rollback Plan

Every implementation phase should be independently revertible.

Rollback rules:

- P1 can be reverted to restore direct tmux usage if backend abstraction causes
  regressions.
- P2 TCP transport must be isolated so Unix socket behavior remains untouched.
- P3 ConPTY code must be build-tagged so it can be disabled without changing
  macOS/Linux binaries.
- P4 Discord native Windows enablement should be behind platform/preflight
  checks so it can be turned off without removing Discord support elsewhere.
- Database migrations must tolerate binaries that do not use Windows backend
  fields.

If a release ships with the native Windows backend disabled or incomplete, the
`--platform windows` command should fail with a clear preflight error naming the
missing capability, and macOS/Linux/Docker behavior must remain untouched.

## Acceptance Criteria

Native Windows support is ready to advertise when:

- macOS, Linux, and Docker tests remain green.
- Native Windows build and unit tests run in CI.
- A native Windows runtime can start without WSL2 or Docker.
- `agx launch --platform windows --discord-server-id <server>` can start the
  runtime and connect Discord natively on Windows.
- Discord slash commands can create and remove projects and tasks.
- A Discord task can receive a message, run an agent, and show logs.
- Killing or deleting a task cleans up the whole process tree.
- Worktree cleanup failures are logged and returned as warnings/errors.
- A second machine is blocked from attaching to an already-owned Discord server.
- User-facing docs describe `--platform windows` as native Windows, with no WSL2
  Windows path.

## Resolved Decisions

- `--platform windows` means native Windows. There is no `windows-native` flag
  and WSL2 is dropped as a Windows target.
- First-release native agent CLI support targets `claude` and `codex`. Other
  agent CLIs are best effort until validated on a real Windows host.
- Direct/interactive attach is deferred. The MVP supports task create,
  log/snapshot, and kill only.
- ConPTY wrapper: start with `github.com/UserExistsError/conpty` (pure Go, no
  cgo, supports resize, exposes the process handle, small dependency graph),
  isolated to Windows build-tagged files. Process-tree cleanup is NOT provided by
  any ConPTY wrapper and is handled separately by a Windows Job Object. See
  ConPTY Library Evaluation.
- Stale Discord ownership: detection is automatic, takeover is always explicit.
  AGX never silently steals ownership. Split-brain and concurrent takeover are
  prevented by an epoch/generation counter, a compare-and-swap claim, and a
  fencing grace wait. See Runtime And Discord Locks.

## Open Questions

None currently open. New Windows-specific constraints discovered during
implementation should be added here and resolved before the phase that depends on
them.
