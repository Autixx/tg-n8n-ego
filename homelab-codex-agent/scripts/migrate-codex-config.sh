#!/usr/bin/env bash
set -euo pipefail

SERVICE_USER="codexagent"
SERVICE_GROUP="codexagent"
SOURCE_DIR="/home/${SERVICE_USER}/.codex"
TARGET_DIR="/opt/codex-agent/.codex"
FORCE=false

if [[ "${1:-}" == "--force" ]]; then
  FORCE=true
elif [[ $# -gt 0 ]]; then
  echo "Usage: sudo ./scripts/migrate-codex-config.sh [--force]" >&2
  exit 2
fi

if [[ "${EUID}" -ne 0 ]]; then
  if command -v sudo >/dev/null 2>&1; then
    echo "migrate-codex-config.sh requires root; re-running with sudo." >&2
    exec sudo -- bash "${BASH_SOURCE[0]}" "$@"
  fi
  echo "migrate-codex-config.sh requires root." >&2
  exit 1
fi

if ! id -u "${SERVICE_USER}" >/dev/null 2>&1; then
  echo "Service user ${SERVICE_USER} does not exist; run scripts/install.sh first." >&2
  exit 1
fi

install -d -m 0700 -o "${SERVICE_USER}" -g "${SERVICE_GROUP}" "${TARGET_DIR}"
if [[ -d "${SOURCE_DIR}" ]]; then
  rsync_options=(-a)
  if [[ "${FORCE}" == false ]]; then
    rsync_options+=(--ignore-existing)
  fi
  rsync "${rsync_options[@]}" "${SOURCE_DIR}/" "${TARGET_DIR}/"
  echo "Migrated Codex config from ${SOURCE_DIR} to ${TARGET_DIR} (force=${FORCE})."
elif [[ -z "$(find "${TARGET_DIR}" -mindepth 1 -print -quit)" ]]; then
  echo "Warning: no Codex config found in ${SOURCE_DIR}; authenticate/configure Codex with HOME=${TARGET_DIR%/.codex}." >&2
else
  echo "No legacy Codex config found; existing ${TARGET_DIR} was preserved."
fi

chown -R "${SERVICE_USER}:${SERVICE_GROUP}" "${TARGET_DIR}"
find "${TARGET_DIR}" -type d -exec chmod 0700 {} +
find "${TARGET_DIR}" -type f -perm /0111 -exec chmod 0700 {} +
find "${TARGET_DIR}" -type f ! -perm /0111 -exec chmod 0600 {} +
