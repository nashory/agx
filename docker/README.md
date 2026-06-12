# AGX Docker

This directory contains the Docker assets for running AGX in an Ubuntu
container. The container mode is intended for Linux users and for users on any
Docker-capable host who want AGX without the macOS Desktop app.

## Quick Start

From the repository root:

```bash
make -C docker build
make -C docker start
make -C docker shell
```

Open the TUI:

```bash
make -C docker tui
```

Run a one-off interactive shell without keeping a named runtime container:

```bash
make -C docker run
```

Run the noninteractive image smoke checks used by CI:

```bash
make -C docker smoke
```

## Useful Variables

```bash
make -C docker build IMAGE=agx:dev
make -C docker start PROJECT=/path/to/project
make -C docker shell AGX_HOME="$HOME/.agx-docker"
make -C docker exec CMD='agx doctor'
make -C docker build PLATFORM=linux/amd64
make -C docker smoke PLATFORM=linux/amd64
```

Defaults:

```text
IMAGE=agx:ubuntu
CONTAINER=agx
PROJECT=<repo root>
WORKDIR=/workspace/<project name>
AGX_HOME=$HOME/.agx-docker
AGX_STATE_VOLUME=agx-data
HOST_UID=$(id -u)
HOST_GID=$(id -g)
```

`AGX_HOME` is mounted as `/home/agx` in the container so agent CLI credentials
and user-level home files can persist across container restarts.

`AGX_STATE_VOLUME` is mounted as `/home/agx/.config/agx` so AGX runtime state
and the Unix socket live on a Docker-managed Linux filesystem. This avoids
socket permission issues on Docker Desktop bind mounts.

The container starts as root only long enough to create a user matching
`HOST_UID:HOST_GID`, then runs AGX commands as that user. This keeps files
created in mounted projects owned by the host user.
