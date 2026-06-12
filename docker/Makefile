SHELL := /usr/bin/env bash

IMAGE ?= agx:ubuntu
CONTAINER ?= agx
PROJECT ?= $(abspath ..)
PROJECT_NAME ?= $(notdir $(PROJECT))
WORKDIR ?= /workspace/$(PROJECT_NAME)
AGX_HOME ?= $(HOME)/.agx-docker
AGX_STATE_VOLUME ?= agx-data
HOST_UID ?= $(shell id -u)
HOST_GID ?= $(shell id -g)
VERSION ?= dev
COMMIT ?= $(shell git -C .. rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
CMD ?= bash
PLATFORM ?=
TTY_ARGS ?= $(shell test -t 0 && printf -- "-it" || true)

DOCKER ?= docker
DOCKERFILE := Dockerfile
CONTEXT := ..
PLATFORM_ARG := $(if $(PLATFORM),--platform $(PLATFORM),)

RUN_ARGS := \
	--name $(CONTAINER) \
	--init \
	-e HOME=/home/agx \
	-e AGX_CONFIG_DIR=/home/agx/.config/agx \
	-e HOST_UID=$(HOST_UID) \
	-e HOST_GID=$(HOST_GID) \
	-v "$(AGX_HOME):/home/agx" \
	-v "$(AGX_STATE_VOLUME):/home/agx/.config/agx" \
	-v "$(PROJECT):$(WORKDIR)" \
	-w "$(WORKDIR)"

EXEC_ARGS := \
	--user $(HOST_UID):$(HOST_GID) \
	-e HOME=/home/agx \
	-e AGX_CONFIG_DIR=/home/agx/.config/agx

.PHONY: help init build rebuild run start restart stop rm clean clean-volume shell exec tui logs status doctor runtime-status smoke print-config

help:
	@printf '%s\n' 'AGX Docker targets:'
	@printf '  %-16s %s\n' 'make build' 'Build the Ubuntu AGX image'
	@printf '  %-16s %s\n' 'make run' 'Run a disposable interactive shell'
	@printf '  %-16s %s\n' 'make start' 'Start a named runtime container in the background'
	@printf '  %-16s %s\n' 'make shell' 'Open a shell in the running container'
	@printf '  %-16s %s\n' 'make tui' 'Open agx tui in the running container'
	@printf '  %-16s %s\n' 'make exec CMD=...' 'Run a command in the running container'
	@printf '  %-16s %s\n' 'make logs' 'Follow runtime container logs'
	@printf '  %-16s %s\n' 'make stop' 'Stop the runtime container'
	@printf '  %-16s %s\n' 'make clean' 'Remove the runtime container'
	@printf '  %-16s %s\n' 'make smoke' 'Build and run noninteractive Docker smoke checks'
	@printf '\n%s\n' 'Useful variables:'
	@printf '  IMAGE=%s\n' '$(IMAGE)'
	@printf '  CONTAINER=%s\n' '$(CONTAINER)'
	@printf '  PROJECT=%s\n' '$(PROJECT)'
	@printf '  AGX_HOME=%s\n' '$(AGX_HOME)'
	@printf '  AGX_STATE_VOLUME=%s\n' '$(AGX_STATE_VOLUME)'
	@printf '  PLATFORM=%s\n' '$(PLATFORM)'

init:
	@mkdir -p "$(AGX_HOME)"

build:
	$(DOCKER) build \
		$(PLATFORM_ARG) \
		-f $(DOCKERFILE) \
		-t $(IMAGE) \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(DATE) \
		$(CONTEXT)

rebuild:
	$(DOCKER) build --no-cache \
		$(PLATFORM_ARG) \
		-f $(DOCKERFILE) \
		-t $(IMAGE) \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(DATE) \
		$(CONTEXT)

run: init
	$(DOCKER) run --rm $(TTY_ARGS) $(RUN_ARGS) $(IMAGE) bash

start: init
	@if $(DOCKER) ps -a --format '{{.Names}}' | grep -qx '$(CONTAINER)'; then \
		if $(DOCKER) ps --format '{{.Names}}' | grep -qx '$(CONTAINER)'; then \
			echo 'container $(CONTAINER) is already running'; \
		else \
			$(DOCKER) start $(CONTAINER) >/dev/null; \
			echo 'started existing container $(CONTAINER)'; \
		fi; \
	else \
		$(DOCKER) run -d $(RUN_ARGS) $(IMAGE) agx runtime start >/dev/null; \
		echo 'started new container $(CONTAINER)'; \
	fi

restart: stop start

stop:
	@if $(DOCKER) ps --format '{{.Names}}' | grep -qx '$(CONTAINER)'; then \
		$(DOCKER) stop $(CONTAINER) >/dev/null; \
		echo 'stopped container $(CONTAINER)'; \
	else \
		echo 'container $(CONTAINER) is not running'; \
	fi

rm:
	@if $(DOCKER) ps -a --format '{{.Names}}' | grep -qx '$(CONTAINER)'; then \
		$(DOCKER) rm -f $(CONTAINER) >/dev/null; \
		echo 'removed container $(CONTAINER)'; \
	else \
		echo 'container $(CONTAINER) does not exist'; \
	fi

clean: rm

clean-volume: rm
	@if $(DOCKER) volume inspect "$(AGX_STATE_VOLUME)" >/dev/null 2>&1; then \
		$(DOCKER) volume rm "$(AGX_STATE_VOLUME)" >/dev/null; \
		echo 'removed volume $(AGX_STATE_VOLUME)'; \
	else \
		echo 'volume $(AGX_STATE_VOLUME) does not exist'; \
	fi

shell: start
	$(DOCKER) exec $(TTY_ARGS) $(EXEC_ARGS) $(CONTAINER) bash

exec: start
	$(DOCKER) exec $(TTY_ARGS) $(EXEC_ARGS) $(CONTAINER) $(CMD)

tui: start
	$(DOCKER) exec $(TTY_ARGS) $(EXEC_ARGS) $(CONTAINER) agx tui

logs: start
	$(DOCKER) logs -f $(CONTAINER)

status:
	@$(DOCKER) ps -a --filter name='^/$(CONTAINER)$$'

doctor: start
	$(DOCKER) exec $(EXEC_ARGS) $(CONTAINER) agx doctor

runtime-status: start
	$(DOCKER) exec $(EXEC_ARGS) $(CONTAINER) agx runtime status

smoke: build
	$(DOCKER) run --rm \
		$(PLATFORM_ARG) \
		-e HOST_UID=$(HOST_UID) \
		-e HOST_GID=$(HOST_GID) \
		$(IMAGE) \
		bash -c 'set -euo pipefail; \
			test "$$HOME" = "/home/agx"; \
			test "$$AGX_CONFIG_DIR" = "/home/agx/.config/agx"; \
			agx --version; \
			agx tui --once >/tmp/agx-tui.out; \
			tmux -V; \
			git --version; \
			agx doctor >/tmp/agx-doctor.out; \
			grep -q "systemd unit: /home/agx/.config/systemd/user/dev.agx.runtime.service" /tmp/agx-doctor.out'
	$(DOCKER) run --rm \
		$(PLATFORM_ARG) \
		-e HOST_UID=0 \
		-e HOST_GID=0 \
		$(IMAGE) \
		bash -c 'set -euo pipefail; \
			test "$$(id -u)" = "0"; \
			test "$$HOME" = "/home/agx"; \
			agx --version >/dev/null'

print-config:
	@printf 'IMAGE=%s\n' '$(IMAGE)'
	@printf 'CONTAINER=%s\n' '$(CONTAINER)'
	@printf 'PROJECT=%s\n' '$(PROJECT)'
	@printf 'WORKDIR=%s\n' '$(WORKDIR)'
	@printf 'AGX_HOME=%s\n' '$(AGX_HOME)'
	@printf 'AGX_STATE_VOLUME=%s\n' '$(AGX_STATE_VOLUME)'
	@printf 'HOST_UID=%s\n' '$(HOST_UID)'
	@printf 'HOST_GID=%s\n' '$(HOST_GID)'
	@printf 'PLATFORM=%s\n' '$(PLATFORM)'
	@printf 'TTY_ARGS=%s\n' '$(TTY_ARGS)'
