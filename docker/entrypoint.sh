#!/usr/bin/env bash
set -euo pipefail

export HOME="${HOME:-/home/agx}"
export AGX_CONFIG_DIR="${AGX_CONFIG_DIR:-${HOME}/.config/agx}"
AGX_USER="${AGX_USER:-agx}"
HOST_UID="${HOST_UID:-1000}"
HOST_GID="${HOST_GID:-1000}"

if [[ "$(id -u)" == "0" && "${AGX_DOCKER_GOSU_DONE:-0}" != "1" ]]; then
  group_name="$(getent group "${HOST_GID}" | cut -d: -f1 || true)"
  if [[ -z "${group_name}" ]]; then
    group_name="${AGX_USER}"
    if getent group "${group_name}" >/dev/null; then
      group_name="${AGX_USER}-${HOST_GID}"
    fi
    groupadd --gid "${HOST_GID}" "${group_name}"
  fi

  user_name="$(getent passwd "${HOST_UID}" | cut -d: -f1 || true)"
  if [[ -z "${user_name}" ]]; then
    user_name="${AGX_USER}"
    if getent passwd "${user_name}" >/dev/null; then
      user_name="${AGX_USER}-${HOST_UID}"
    fi
    useradd \
      --key UID_MIN=0 \
      --key UID_MAX=60000 \
      --uid "${HOST_UID}" \
      --gid "${HOST_GID}" \
      --home-dir "${HOME}" \
      --shell /bin/bash \
      --no-create-home \
      "${user_name}"
  fi

  mkdir -p "${HOME}" "${AGX_CONFIG_DIR}" "${AGX_CONFIG_DIR}/logs"
  chown "${HOST_UID}:${HOST_GID}" "${HOME}" "${AGX_CONFIG_DIR}" "${AGX_CONFIG_DIR}/logs" 2>/dev/null || true

  exec gosu "${HOST_UID}:${HOST_GID}" env \
    AGX_DOCKER_GOSU_DONE=1 \
    HOME="${HOME}" \
    AGX_CONFIG_DIR="${AGX_CONFIG_DIR}" \
    USER="${user_name}" \
    "$0" "$@"
fi

mkdir -p "${HOME}" "${AGX_CONFIG_DIR}" "${AGX_CONFIG_DIR}/logs"

if [[ $# -eq 0 ]]; then
  exec bash
fi

exec "$@"
