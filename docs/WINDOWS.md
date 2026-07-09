# Native Windows

AGX runs natively on Windows — no WSL2 or Docker. This is a preview; the full
design and rationale live in [WINDOWS_NATIVE_SUPPORT_DESIGN.md](WINDOWS_NATIVE_SUPPORT_DESIGN.md).

## Requirements

On the Windows `PATH`:

- `git`
- `powershell` (Windows PowerShell is built in)
- at least one agent CLI — `claude` and/or `codex` are the first-release targets
- Windows 10/11 (ConPTY is required and is checked at launch)

## How it differs from macOS/Linux

| Concern | macOS/Linux | Native Windows |
| --- | --- | --- |
| Runtime transport | Unix socket | authenticated localhost TCP (token in the config dir) |
| Session engine | tmux | ConPTY + Windows Job Object |
| Task command | POSIX shell script | PowerShell script |
| Locks | `flock` | `LockFileEx` |
| Service | launchd / systemd user | Windows Service (SCM) |

The runtime binds loopback only and rejects any request without the runtime
token, even on `127.0.0.1`.

## Quickstart

```powershell
go build -o agx.exe ./cmd/agx

# Foreground runtime (good for first runs / debugging)
./agx.exe runtime start

# In another terminal
./agx.exe runtime status
./agx.exe doctor
```

Launch the runtime and connect Discord:

```powershell
./agx.exe launch --platform windows --discord-server-id <server-id>
```

`--platform windows` means native Windows. Inside a WSL2 shell use
`--platform linux` instead. When no Windows service is installed, `launch` starts
the runtime as a detached background process.

## Windows service (optional)

```powershell
# Run from an elevated (Administrator) PowerShell
./agx.exe runtime install-service
./agx.exe runtime uninstall-service
```

The service runs under the Service Control Manager and is pinned to the
installing user's AGX config directory. The service account (LocalSystem by
default) differs from your interactive user; if the CLI and service do not see
the same state, reconfigure the service to run under your own account.

## Discord ownership takeover

If a machine that owned the Discord server died, its owner record goes stale.
AGX never steals ownership automatically. To reclaim it explicitly:

```powershell
./agx.exe launch --platform windows --discord-server-id <server-id> --take-discord-ownership
```

Takeover refuses a still-alive owner, bumps the ownership epoch, and re-verifies
after a short fencing delay so a returning owner is not clobbered.

## Logs, config, recovery

- Config directory and runtime/error logs are shown by `agx doctor` and
  `agx runtime status`.
- The runtime transport token is stored in the config directory; it is
  regenerated on each start.
- ConPTY tasks are in-memory: after a runtime restart, previously running tasks
  are marked offline (recovery is limited by design in this preview).

## Manual validation checklist

```
build:        go build -o agx.exe ./cmd/agx
runtime:      agx runtime start   →   agx runtime status (running, over TCP)
project:      /project create <name>
task:         /task create <project> <prompt>
observe:      task output streams; send input; interrupt
kill:         kill the task; confirm no orphaned processes in Task Manager
ownership:    a second machine cannot control an already-owned server
```

Interactive behavior (ConPTY rendering, injected-prompt readiness, agent-CLI
quoting) should be validated on a real Windows terminal.
