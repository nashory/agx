# Linux Support Design

This document defines the planned Linux support model for AGX, including native
Linux runtime/CLI support, TUI support, Docker-based Ubuntu support, packaging,
CI, documentation, and compatibility requirements.

The primary goal is not to port the macOS Desktop app to every Linux desktop
environment first. The primary goal is to make AGX a reliable Unix-native tool
on Linux, with `agx tui` as the Linux UI. Docker is the first portable packaging
path for users on any host who want an Ubuntu-based AGX environment.

## Goals

- Support native Linux CLI/runtime operation.
- Support native Linux service management through `systemd --user`.
- Provide a first-class Linux control surface through `agx tui`.
- Provide a Docker-packaged Ubuntu runtime mode that works on Linux, macOS, and
  other hosts with Docker.
- Provide Linux release artifacts that can be installed without building from
  source.
- Preserve all existing macOS native functionality and Desktop compatibility.
- Keep the runtime daemon as the single owner of SQLite state, tmux sessions,
  agent processes, Discord sync, and task recovery.
- Avoid requiring Wails, GTK, WebKitGTK, or Linux desktop packaging for the
  first Linux release.
- Let users without macOS Desktop support run AGX through Docker and a terminal.
- Keep host files safe by making project mounts, credentials, and path mapping
  explicit.

## Non-Goals

- Running the Wails Desktop app inside Docker.
- Shipping Linux Desktop packages before the runtime/TUI path is stable.
- Supporting arbitrary container base images for the first release.
- Hiding Docker semantics completely. Users should understand what project and
  credential directories are mounted.
- Sharing one mutable AGX config directory between host-native and container
  modes without explicit migration.
- Replacing the macOS Desktop app with the TUI.
- Changing macOS launchd behavior while adding Linux systemd or Docker support.
- Requiring Docker for native Linux users.
- Requiring systemd inside Docker containers.

## Support Matrix

```text
macOS native:
  - Desktop app: supported
  - CLI/runtime: supported
  - TUI: initial runtime dashboard implemented
  - Docker runtime: implemented

Linux native:
  - Desktop app: not first target
  - CLI/runtime: implemented
  - TUI: initial primary UI implemented
  - Docker runtime: implemented

Other Docker hosts:
  - Desktop app: not supported
  - Host CLI wrapper: planned
  - Container runtime + TUI: implemented through docker/Makefile
```

## Scope

Linux support has four related but separate tracks:

1. Native Linux CLI/runtime.
2. Native Linux TUI.
3. Ubuntu Docker runtime/TUI for any Docker-capable host.
4. Release, CI, packaging, and documentation for the above.

The first stable Linux claim should be:

```text
AGX supports Linux through the CLI/runtime and TUI.
AGX also provides a Docker-based Ubuntu runtime for users who prefer an isolated
or host-independent environment.
```

The first stable Linux claim should not include:

```text
AGX supports Linux Desktop.
```

## Compatibility Requirements

Linux and Docker support must be additive.

Existing macOS native behavior is a compatibility target, not an implementation
detail to replace. Any implementation work should preserve:

- macOS Desktop startup, project registration, task management, terminal
  streaming, file browsing, prompt composition, Discord sync, and runtime
  controls.
- macOS `launchd` install/uninstall/status behavior.
- Existing config paths under `~/.config/agx`.
- Existing runtime Unix socket behavior for local Desktop and CLI clients.
- Existing tmux session naming and recovery semantics.
- Existing Discord channel sync semantics.
- Existing task/worktree database schema semantics unless a migration is added.

Changes that add Linux or Docker behavior should use OS-specific backends,
shared interfaces, or new commands. They should not change macOS code paths as a
side effect.

Practical guardrails:

- Keep darwin launchd code in darwin-specific files or behind a darwin backend.
- Add Linux systemd code as a separate backend.
- Add Docker mode as an explicit command group or runtime mode.
- Keep TUI code separate from Desktop code; share business logic through
  runtime APIs and small reusable packages.
- Do not make Docker paths the default for native macOS.
- Do not make TCP runtime listening the default for native macOS.
- Keep existing Desktop tests and macOS packaging tests green while adding
  Linux/Docker tests.

The intended end state is:

```text
macOS native users:
  keep using AGX Desktop and CLI exactly as before.

Linux native users:
  use AGX TUI and CLI with systemd user service support.

Docker users on any host:
  use AGX TUI and CLI inside an Ubuntu container with explicit mounts.
```

## Architecture

### Native Linux

```text
Linux terminal
    │
    ├── agx tui
    ├── agx runtime start
    ├── agx runtime install-service
    └── agx task, logs, chat, and attach commands
            │
            ▼
    AGX runtime daemon
            │
            ├── ~/.config/agx/agx.db
            ├── ~/.config/agx/runtime.sock
            ├── tmux -L agx
            ├── git worktrees
            ├── agent CLI processes
            └── optional Discord bridge
```

Native Linux should use the same runtime API and data model as macOS. The
service manager changes from launchd to systemd user services; the core runtime
does not become Linux-specific.

### Docker

```text
Host terminal
    │
    │ make -C docker tui
    │ make -C docker shell
    │ make -C docker exec CMD='...'
    ▼
Docker container: agx-ubuntu
    │
    ├── agx runtime start
    ├── agx tui
    ├── tmux -L agx
    ├── git
    ├── agent CLIs
    └── ~/.config/agx
            ├── agx.db
            ├── config.toml
            ├── runtime.sock
            ├── logs/
            └── streams/

Mounted project:
    host path      -> container path
    /host/project  -> /workspace/project
```

The container is the AGX machine. Runtime state, tmux sessions, task worktrees,
and agent subprocesses live inside the container. Host integration is limited to
project mounts, optional credential mounts, and wrapper commands.

## Platform Boundaries

Linux support should be added by isolating platform-specific behavior.

```text
cmd/agx
  runtime install-service/status/uninstall-service commands
      │
      ▼
internal/runtime/service_manager
      ├── launchd backend       darwin
      ├── systemd user backend  linux
      └── foreground backend    docker/no-service

internal/desktop
  macOS Desktop behavior remains darwin/native

internal/tui
  terminal UI over runtime client APIs

docker/
  Dockerfile, entrypoint, README, and Makefile for the first Docker UX

internal/docker
  future host-side `agx docker ...` wrapper helpers
```

Platform-specific code should be selected through build tags, runtime GOOS
checks, or explicit mode flags. The preference is build-tagged files for service
manager and OS integration code, and runtime mode flags for Docker behavior.

## Native Linux Runtime

The current runtime design is already close to Linux-compatible:

- Runtime API over a local Unix socket.
- Runtime lock through advisory file locking.
- SQLite database in the AGX config directory.
- tmux-backed session orchestration.
- git worktree support.
- agent CLIs launched as child processes.

Native Linux work should focus on removing macOS assumptions around service
management and project access repair.

### Filesystem Layout

Default Linux layout should match the current config behavior:

```text
~/.config/agx/
  config.toml
  agx.db
  runtime.sock
  runtime.lock
  discord.lock
  logs/
  streams/
  worktrees/
```

`AGX_CONFIG_DIR` should continue to override the config directory on all
platforms.

### Dependencies

Required Linux runtime dependencies:

```text
tmux
git
ca-certificates
```

Recommended TUI/runtime dependencies:

```text
ripgrep
less
openssh-client
```

Agent dependencies are user-managed:

```text
claude
codex
gemini
cursor
copilot
opencode
```

AGX should report missing dependencies through `agx doctor` and the TUI status
surface. It should not try to install system packages without explicit user
request.

## Native Linux Service Management

Native Linux should use user-level systemd.

Unit path:

```text
~/.config/systemd/user/dev.agx.runtime.service
```

Unit shape:

```ini
[Unit]
Description=AGX Runtime

[Service]
Type=simple
ExecStart=/path/to/agx runtime start
Restart=always
RestartSec=2
Environment=PATH=...
Environment=HOME=...

[Install]
WantedBy=default.target
```

Install flow:

```bash
mkdir -p ~/.config/systemd/user
write dev.agx.runtime.service
systemctl --user daemon-reload
systemctl --user enable dev.agx.runtime.service
systemctl --user start dev.agx.runtime.service
```

Uninstall flow:

```bash
systemctl --user stop dev.agx.runtime.service
systemctl --user disable dev.agx.runtime.service
rm ~/.config/systemd/user/dev.agx.runtime.service
systemctl --user daemon-reload
```

Status flow:

```bash
systemctl --user is-active dev.agx.runtime.service
systemctl --user status dev.agx.runtime.service
```

The CLI command names should stay the same:

```bash
agx runtime install-service
agx runtime uninstall-service
agx runtime status
```

The backend implementation changes per OS.

## Linux Packaging and Release

Initial Linux release artifacts should be conservative:

```text
agx-linux-amd64.tar.gz
agx-linux-arm64.tar.gz
checksums.txt
```

Each tarball should contain:

```text
agx
LICENSE
README.md or install notes
```

Later packages:

```text
.deb package
Homebrew formula for Linuxbrew
container image ghcr.io/nashory/agx:ubuntu
```

The first release does not need `.rpm`, AppImage, or Linux Desktop packages.

Release assets:

```text
scripts/package-linux.sh
scripts/package-macos.sh
scripts/release-checksums.sh
docker/Dockerfile
docker/Makefile
```

Make targets:

```text
make package-linux
make package-macos
make release-checksums
make docker-image
```

Implementation status:

- `scripts/package-linux.sh` builds `agx-linux-amd64.tar.gz` and
  `agx-linux-arm64.tar.gz`.
- `make package-linux` runs the Linux tarball packaging flow.
- `make release-checksums` writes a checksum file for the complete release
  artifact set.
- `make docker-image` delegates to `docker/Makefile`.
- `.github/workflows/ci.yml` validates Linux Go tests, Linux packaging,
  frontend build, and Docker image build.

Linux release validation:

```bash
tar -xzf dist/agx-linux-amd64.tar.gz
./agx --version
./agx doctor
```

## Linux CI

Add Ubuntu CI before claiming Linux support.

Required jobs:

```text
ubuntu-latest:
  go test ./cmd/agx ./internal/...
  go build ./cmd/agx
```

Frontend regression jobs should remain separate from the core Linux support
gate:

```text
frontend:
  npm --prefix desktop/frontend ci
  npm --prefix desktop/frontend run build
```

Optional jobs:

```text
ubuntu-latest with tmux:
  install tmux git
  run runtime/session tests that require Unix tools

ubuntu-latest docker:
  docker build
  container smoke test
```

macOS CI must remain present for Desktop and launchd behavior. Linux support
must not replace macOS validation.

## Linux Doctor Behavior

`agx doctor` should become platform-aware.

Native Linux checks:

- Runtime socket reachable.
- Runtime lock path.
- Config directory mode.
- systemd user service installed/active if systemd is available.
- `tmux` present.
- `git` present.
- configured agent commands present.
- Discord config masked and valid enough to report.

Docker checks:

- Running inside container or host wrapper mode.
- Docker CLI present for host wrapper.
- AGX state volume present.
- Project mount writable.
- UID/GID ownership looks sane.
- configured agent commands present inside the container.

macOS checks should keep launchd reporting.

## Linux Project Access

Linux project access should be ordinary Unix filesystem access.

Native Linux:

- Validate read/write access by temp-file probe.
- Report permission errors directly.
- Do not run macOS `xattr`.
- Do not attempt `sudo`.

Docker:

- Validate the container path is writable.
- If not writable, report UID/GID and mount guidance.
- Keep host path mapping explicit for host-side wrappers.

## Linux Agent Setup

AGX should support agents in Linux by executing configured commands on PATH.

Principles:

- Do not bundle closed or third-party agent CLIs without a deliberate packaging
  decision.
- Do not auto-mount host credentials.
- Support container-internal login as the default Docker credential path.
- Support explicit credential mounts for advanced users.
- Let users override agent command/args in config.

`agx doctor` and TUI should distinguish:

```text
agent command missing
agent command present, auth unknown
agent command present, smoke test passed
```

Authentication smoke tests are agent-specific and should be added only when
stable, non-invasive commands exist.

## Linux Desktop Position

Linux Desktop is intentionally deferred.

Reasons:

- Wails Linux depends on GTK/WebKitGTK.
- Linux desktop packaging differs by distro.
- Docker plus Linux GUI forwarding is fragile.
- TUI covers the main AGX workflows and works over SSH and Docker.

Future Linux Desktop support is allowed, but it should be a separate track after
native CLI/runtime, TUI, and Docker are stable.

## Control Surfaces

### TUI

The TUI is the primary Linux UI.

It should use the runtime API, not the database directly. This preserves one
source of truth across CLI, TUI, Discord, and Desktop.

Initial command:

```bash
agx tui
```

The initial `agx tui` implementation is a runtime-backed dashboard. It shows
runtime health, project count, Discord state, active tasks, and recent tasks.
It supports `r` refresh and `q` quit, plus `agx tui --once` for noninteractive
diagnostics and Docker smoke checks.

Initial Docker interface:

```bash
make -C docker tui
```

Future Docker wrapper:

```bash
agx docker tui
```

The wrapper should attach to the container and run:

```bash
docker exec -it agx agx tui
```

### CLI

The normal CLI remains available on native Linux and inside the container:

```bash
agx runtime status
agx task list
docker exec -it agx agx runtime status
docker exec -it agx agx task list
```

The host wrapper should provide shortcuts for common commands, but it should not
invent a separate behavior model.

### Discord

Discord continues to run from the runtime daemon. Container networking only
needs outbound access to Discord APIs.

Discord bot tokens and guild IDs must be configured inside the native/container
AGX config or mounted through an explicit credentials path. The current CLI
surface for Discord configuration is `agx chat ...`; a future rename to
`agx discord ...` would need its own migration plan.

## TUI Feature Parity

The target is feature-equivalent workflows, not pixel-equivalent Desktop UI.

| Desktop capability | TUI equivalent |
| --- | --- |
| Project list | Project pane |
| Project registration | Path entry, directory browser, or recent mount picker |
| Project discovery | Directory scan command with fuzzy picker |
| Task list | Task pane with status badges |
| Task create/run/stop/delete | Command palette and key bindings |
| Live terminal | Log pane plus optional tmux attach |
| Send input | Focused input bar |
| Transcript view | Transcript pane |
| File browser | Directory pane |
| File preview | Text/code/markdown preview pane |
| Markdown render | Terminal markdown renderer |
| Prompt compose with files | Context selection plus prompt editor |
| Runtime status | Status bar and runtime pane |
| Discord status/sync | Discord pane and command palette |
| Settings | Config pane for high-value fields |

The TUI should start small and remain operable over SSH and inside Docker.

## Initial TUI Layout

```text
+ Projects ---------+ + Tasks --------------------+ + Detail -------------------+
| agx               | | active  implement docker  | | transcript/log/file view  |
| website           | | waiting fix sync          | |                           |
| infra             | | done    update docs       | | prompt input              |
+-------------------+ +--------------------------+ +---------------------------+
| runtime ok | discord connected | agent claude | /workspace/project          |
+---------------------------------------------------------------------------+
```

Core key model:

```text
tab        next pane
shift-tab  previous pane
n          new task
r          run/resume task
s          stop task
d          delete task
enter      open detail
/          search/filter
:          command palette
ctrl-t     tmux attach
ctrl-l     logs
ctrl-f     files
ctrl-d     discord
q          quit
```

## Docker Runtime Model

### Container Image

Docker assets should live under `docker/`:

```text
docker/
  Dockerfile
  entrypoint.sh
  README.md
```

The repository root should not contain the primary Dockerfile. Keeping Docker
assets under `docker/` leaves the macOS/native build layout unchanged and makes
container-specific scripts easier to review.

Base image:

```text
ubuntu:24.04
```

Required packages:

```text
ca-certificates
curl
git
openssh-client
tmux
bash
zsh
less
ripgrep
sqlite3
```

Build-time tools depend on whether the image ships prebuilt `agx` binaries or
builds from source. The first production image should copy a release binary
rather than compile AGX at container startup.

### Runtime User

The container should not run agent tasks as root.

The preferred runtime model is:

1. Start the container entrypoint as root.
2. Pass `HOST_UID` and `HOST_GID` from the host.
3. Create or reuse a passwd/group entry for that UID/GID.
4. Drop privileges with `gosu`.
5. Run AGX, tmux, git, and agent CLIs as the host-mapped user.

This gives Linux tools a real user entry while keeping mounted project files
owned by the host user.

Example launch shape:

```bash
docker run --rm -it \
  --name agx \
  -e HOME=/home/agx \
  -e HOST_UID="$(id -u)" \
  -e HOST_GID="$(id -g)" \
  -v "$HOME/.agx-docker:/home/agx" \
  -v agx-data:/home/agx/.config/agx \
  -v "$PWD:/workspace/project" \
  -w /workspace/project \
  ghcr.io/nashory/agx:ubuntu
```

UID/GID matching reduces host file ownership surprises when agents modify a
mounted project. Persisting `/home/agx` lets agent CLI credentials survive
container restarts. Persisting `~/.config/agx` through a Docker-managed named
volume keeps the AGX runtime socket and SQLite state on a Linux filesystem,
which avoids Unix socket permission problems on Docker Desktop bind mounts.

### State

Default container home and state:

```text
/home/agx                    host bind mount for credentials and shell state
/home/agx/.config/agx        Docker named volume for AGX runtime state
```

Recommended persistence:

```text
$HOME/.agx-docker:/home/agx
agx-data:/home/agx/.config/agx
```

This keeps agent CLI credentials in an inspectable host directory while keeping
runtime DB, logs, config, Discord config, generated runtime state, and
`runtime.sock` on a Docker-managed Linux filesystem. Users may choose custom
volume names per AGX environment.

### Project Mounts

Every project must have an explicit host-to-container mount.

```text
host:      /Users/alex/src/project
container: /workspace/project
```

AGX stores the container path in the runtime database. Host-side tools that need
to open a file must use path mapping.

For container-local TUI usage, path mapping can be deferred because file viewing
and task execution happen inside the container.

For future host Desktop + container runtime, path mapping is required:

```toml
[container]
mode = "docker"

[[container.mounts]]
host = "/Users/alex/src/project"
container = "/workspace/project"
```

### Credentials

Agent credentials must be explicit.

Supported approaches:

- Log in to agent CLIs inside the container.
- Mount specific credential directories read-only.
- Pass provider-specific environment variables.

The first release should document known credential paths per agent, but avoid
auto-mounting sensitive host directories.

Example credential mount:

```bash
docker run --rm -it \
  -v "$HOME/.agx-docker:/home/agx" \
  -v agx-data:/home/agx/.config/agx \
  -v "$HOME/.config/agent-cli:/home/agx/.config/agent-cli:ro" \
  ...
```

Discord bot tokens should be configured through `agx chat connect` or TUI
settings inside the container.

## Docker Interface

The first Docker interface is the Makefile under `docker/`:

```bash
make -C docker build
make -C docker start
make -C docker shell
make -C docker tui
make -C docker exec CMD='agx doctor'
make -C docker stop
```

Later, add a host-side command group:

```bash
agx docker init
agx docker start
agx docker stop
agx docker status
agx docker tui
agx docker exec -- agx runtime status
agx docker shell
```

The Makefile and future wrapper should:

- Create a named container if needed.
- Create/use a dedicated mounted AGX home directory for credentials and shell
  state.
- Create/use a Docker named volume for AGX runtime state and sockets.
- Mount the selected project into `/workspace/<name>`.
- Pass UID/GID when possible.
- Refuse to mount broad sensitive directories by default.
- Print the exact `docker run` command in verbose mode.

The wrapper does not need Docker API bindings initially. Shelling out to the
Docker CLI is acceptable and transparent.

## Runtime API Access

For container-only usage, the runtime can keep using its Unix socket inside the
container.

For future host Desktop or host CLI talking to container runtime, add an
optional TCP listener:

```bash
agx runtime start --listen unix
agx runtime start --listen tcp://127.0.0.1:8765
```

Security rules:

- TCP listening must be disabled by default.
- TCP listener must bind to loopback by default.
- Every TCP listener must require an auth token, even on loopback.
- Non-loopback bind must be refused unless an explicit unsafe flag and auth
  token are both provided.
- Runtime clients must validate the configured bind address before connecting.
- Runtime requests accepted over TCP should be auditable in logs.

Docker port mapping should be opt-in:

```bash
docker run -p 127.0.0.1:8765:8765 ...
```

The first Docker TUI release does not require TCP because the TUI runs inside
the same container as the runtime.

## Service Manager Interface

Native Linux support should use `systemd --user`, as described above. Docker
mode should not require systemd inside the container. The container entrypoint
can run the runtime directly, or the wrapper can start it on demand:

```bash
agx runtime start
```

The user-facing service commands stay platform-neutral:

```bash
agx runtime install-service
agx runtime uninstall-service
agx runtime status
```

Implementation should split service management behind an interface:

```text
ServiceManager
  Install(executable, env) error
  Uninstall() error
  Status() ServiceStatus
  Start() error
```

Backends:

```text
darwin: launchd
linux:  systemd --user
docker: no service manager; foreground runtime or container entrypoint
```

The backend split must keep the current macOS launchd implementation behavior
compatible. Refactoring launchd into an interface is acceptable only if the
generated plist, install path, bootstrap/kickstart behavior, and CLI output stay
compatible or are changed deliberately with tests.

## Project Access and File Permissions

macOS project access repair uses `xattr` and optional AppleScript escalation.
That must remain darwin-only.

Linux and Docker behavior:

- Validate write access by creating and deleting a temp file.
- If write access fails, report mount/UID/GID guidance.
- Do not run `xattr`.
- Do not attempt sudo escalation from the TUI.

Common guidance:

```text
The project is not writable from the AGX container.
Restart with HOST_UID/HOST_GID set to your host user, or adjust the project
mount permissions.
```

## Docker Agent Execution

Agents run inside the container in Docker mode.

Implications:

- Agent CLIs must be installed in the image, mounted, or installed by the user.
- Agent authentication must exist inside the container.
- The task workspace path is the container path.
- Host-only GUI flows are unavailable.

Agent command defaults should be audited for Linux. In particular, flags with
OS-specific names should be verified or moved into per-agent config defaults.

## Linux Support Development Phases

### Phase 0: Compatibility Baseline

- Keep `main` macOS behavior green before broad Linux changes.
- Add or preserve tests around launchd plist rendering, service command wiring,
  Desktop runtime controls, project access repair, runtime socket behavior,
  tmux recovery, and Discord sync.
- Record current macOS package behavior so release changes can be compared.

### Phase 1: Native Linux Runtime Boundary

- Split launchd code into darwin-specific files.
- Add a service manager interface.
- Add a Linux systemd user service backend.
- Keep Docker/no-service mode as foreground runtime.
- Make project access repair OS-aware.
- Audit agent default args for Linux compatibility.
- Add Linux CI for `go test ./cmd/agx ./internal/...` on Ubuntu.
- Add `agx doctor` Linux service/dependency reporting.
- Keep Desktop out of the Linux support claim.

### Phase 2: Native Linux Packaging

- Add `scripts/package-linux.sh`.
- Add `make package-linux`.
- Produce `agx-linux-amd64.tar.gz` and `agx-linux-arm64.tar.gz`.
- Add Linux install documentation.
- Add a Linux release smoke test.

### Phase 3: TUI MVP

- Add `agx tui`.
- Use Bubble Tea/Bubbles/Lip Gloss, already present in the dependency graph.
- Read projects/tasks through runtime client APIs.
- Support runtime status, project list, task list, logs, transcript, task
  create/run/stop/delete, and tmux attach.
- Keep file browser and prompt composer out of MVP unless simple.

### Phase 4: Docker Runtime MVP

- Add Dockerfile based on Ubuntu under `docker/Dockerfile`.
- Keep Docker-specific entrypoint and image docs under `docker/`.
- Add `docker/Makefile` targets for build/start/shell/tui/exec/logs/clean.
- Persist `/home/agx` through a dedicated host directory for credentials.
- Persist `/home/agx/.config/agx` through a Docker named volume for AGX runtime
  state and sockets.
- Mount one project at a time under `/workspace/<name>`.
- Run `agx tui` inside the container.
- Document credential setup.

### Phase 5: TUI Feature Expansion

- Add file browser and file preview.
- Add markdown rendering.
- Add prompt composer with selected file context.
- Add Discord connection/sync screens.
- Add project discovery inside mounted workspace roots.

### Phase 6: Host/Container Interop

- Add optional runtime TCP listener.
- Add path mapping config.
- Allow host CLI or future Desktop to talk to container runtime.
- Add mandatory auth for any TCP runtime access.
- Add non-loopback safeguards and request logging.

### Phase 7: Optional Linux Desktop Evaluation

- Evaluate Wails Linux build requirements in an Ubuntu VM.
- Document GTK/WebKitGTK dependencies and packaging constraints.
- Decide whether Linux Desktop adds enough value beyond TUI.
- Keep this separate from the stable Linux support claim.

## Open Questions

- Should the official image include any agent CLIs, or only AGX plus common
  Unix tools?
- Should native Linux install docs prefer tarball, `.deb`, Linuxbrew, or all of
  them over time?
- Should systemd service install enable linger, or only document it for users who
  want runtime startup without an active login session?
- Should Docker mode support multiple mounted projects per container from day
  one, or one project per invocation?
- Should `agx docker tui` run a long-lived named container or a disposable
  container attached to a persistent volume?
- Which markdown renderer should the TUI use?
- Should TUI task terminal mode stream through AGX APIs only, or should it
  provide a direct tmux attach pane?

## Recommended Initial Decision

Start with:

- Native Linux CLI/runtime.
- systemd user service support.
- Linux tarball release artifacts.
- Ubuntu 24.04 image.
- Long-lived named container: `agx`.
- Persistent named volume: `agx-data`.
- One or more explicit project mounts under `/workspace`.
- TUI and runtime both inside the container.
- No TCP runtime listener for the first Docker release.
- No Linux Desktop support claim.

This gives users on Linux, macOS without Desktop, and other Docker-capable hosts
one consistent way to run AGX without solving Linux GUI packaging first.
