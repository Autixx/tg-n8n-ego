#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_DIR="/opt/codex-agent"
ETC_DIR="/etc/codex-agent"
SERVICE_NAME="codex-agent.service"
SERVICE_USER="codexagent"
SERVICE_GROUP="codexagent"
BIN_SRC="${ROOT_DIR}/bin/codex-agent"
BIN_DST="/usr/local/bin/codex-agent"
ADMIN_DST="/usr/local/bin/llm-codex"
CODEX_DST="/usr/local/bin/codex"

ENABLE_SERVICE=false
START_SERVICE=false
SKIP_PACKAGES=false
SKIP_BUILD=false

usage() {
  cat <<'EOF'
Usage: sudo ./scripts/install.sh [options]

Options:
  --enable          Enable codex-agent at boot, but do not start it.
  --start           Enable and start/restart codex-agent after installation.
  --skip-packages   Skip apt update/install (used by upgrade.sh).
  --skip-build      Skip tests/build and use the existing bin/codex-agent.
  -h, --help        Show this help.

Without --enable or --start the installer does not enable or start the service.
EOF
}

for argument in "$@"; do
  case "${argument}" in
    --enable) ENABLE_SERVICE=true ;;
    --start) ENABLE_SERVICE=true; START_SERVICE=true ;;
    --skip-packages) SKIP_PACKAGES=true ;;
    --skip-build) SKIP_BUILD=true ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown option: ${argument}" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ "${EUID}" -ne 0 ]]; then
  if command -v sudo >/dev/null 2>&1; then
    echo "install.sh requires root; re-running with sudo." >&2
    codex_hint="$(command -v codex 2>/dev/null || true)"
    exec sudo -- env CODEX_INSTALL_SOURCE="${codex_hint}" bash "${BASH_SOURCE[0]}" "$@"
  fi
  echo "install.sh requires root. Install sudo or run it as root." >&2
  exit 1
fi

if ! command -v go >/dev/null 2>&1 && [[ -x /usr/local/go/bin/go ]]; then
  export PATH="/usr/local/go/bin:${PATH}"
fi

if [[ "${SKIP_PACKAGES}" == false ]]; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y --no-install-recommends \
    bash bubblewrap ca-certificates curl git jq nano nodejs npm rsync tar whiptail
fi

if [[ "${SKIP_BUILD}" == false ]]; then
  if ! command -v go >/dev/null 2>&1; then
    cat >&2 <<'EOF'
Go is required to build codex-agent but was not found in PATH.
Install Go 1.22 or newer from https://go.dev/doc/install, verify `go version`, then rerun this installer.
EOF
    exit 1
  fi
  (
    cd "${ROOT_DIR}"
    go test ./...
    bash ./scripts/build.sh
  )
elif [[ ! -f "${BIN_SRC}" ]]; then
  echo "${BIN_SRC} does not exist; rerun without --skip-build." >&2
  exit 1
fi

if ! getent group "${SERVICE_GROUP}" >/dev/null 2>&1; then
  groupadd --system "${SERVICE_GROUP}"
fi
if ! id -u "${SERVICE_USER}" >/dev/null 2>&1; then
  useradd \
    --system \
    --gid "${SERVICE_GROUP}" \
    --home-dir "${APP_DIR}" \
    --no-create-home \
    --shell /usr/sbin/nologin \
    "${SERVICE_USER}"
fi

install -d -m 0750 -o "${SERVICE_USER}" -g "${SERVICE_GROUP}" "${APP_DIR}"
install -d -m 0700 -o "${SERVICE_USER}" -g "${SERVICE_GROUP}" "${APP_DIR}/.codex"
for directory in jobs logs prompts schemas projects templates test; do
  install -d -m 0750 -o "${SERVICE_USER}" -g "${SERVICE_GROUP}" "${APP_DIR}/${directory}"
done
install -d -m 0750 -o root -g "${SERVICE_GROUP}" "${ETC_DIR}"

if [[ ! -f "${APP_DIR}/attachment-registry.xml" ]]; then
  printf '%s\n' '<?xml version="1.0" encoding="UTF-8"?>' '<attachmentRegistry></attachmentRegistry>' \
    > "${APP_DIR}/attachment-registry.xml"
fi
chown "${SERVICE_USER}:${SERVICE_GROUP}" "${APP_DIR}/attachment-registry.xml"
chmod 0600 "${APP_DIR}/attachment-registry.xml"

sync_resource() {
  local name="$1"
  if [[ -d "${ROOT_DIR}/${name}" ]]; then
    rsync -a --exclude='.gitkeep' "${ROOT_DIR}/${name}/" "${APP_DIR}/${name}/"
    chown -R "${SERVICE_USER}:${SERVICE_GROUP}" "${APP_DIR}/${name}"
    find "${APP_DIR}/${name}" -type d -exec chmod 0750 {} +
    find "${APP_DIR}/${name}" -type f -exec chmod 0640 {} +
  fi
}

for resource in prompts schemas projects templates test; do
  sync_resource "${resource}"
done

install_executable_atomic() {
  local source="$1"
  local destination="$2"
  local temporary
  temporary="$(mktemp "${destination}.XXXXXX")"
  install -m 0755 -o root -g root "${source}" "${temporary}"
  mv -f "${temporary}" "${destination}"
}

install_executable_atomic "${BIN_SRC}" "${BIN_DST}"
install_executable_atomic "${ROOT_DIR}/scripts/llm-codex.sh" "${ADMIN_DST}"
install -m 0640 -o root -g "${SERVICE_GROUP}" \
  "${ROOT_DIR}/configs/codex-agent.env.example" "${ETC_DIR}/codex-agent.env.example"
if [[ ! -e "${ETC_DIR}/codex-agent.env" ]]; then
  install -m 0640 -o root -g "${SERVICE_GROUP}" \
    "${ROOT_DIR}/configs/codex-agent.env.example" "${ETC_DIR}/codex-agent.env"
  echo "Created ${ETC_DIR}/codex-agent.env; replace placeholder secrets before starting the service."
else
  chown root:"${SERVICE_GROUP}" "${ETC_DIR}/codex-agent.env"
  chmod 0640 "${ETC_DIR}/codex-agent.env"
  echo "Preserved existing ${ETC_DIR}/codex-agent.env."
  if ! grep -Eq '^CODEX_AGENT_CODEX_BIN=/usr/local/bin/codex$' "${ETC_DIR}/codex-agent.env"; then
    echo "Warning: preserved env does not set CODEX_AGENT_CODEX_BIN=/usr/local/bin/codex; update it before restart." >&2
  fi
fi

install -m 0644 -o root -g root \
  "${ROOT_DIR}/configs/codex-agent.service" "/etc/systemd/system/${SERVICE_NAME}"

install_codex_cli() {
  if ! command -v npm >/dev/null 2>&1; then
    cat >&2 <<'EOF'
Codex CLI is missing and npm is not available.
Install Node.js/npm or rerun install.sh without --skip-packages so the installer can install npm.
EOF
    exit 1
  fi
  echo "Codex CLI was not found; installing @openai/codex with npm."
  npm install -g @openai/codex
}

codex_source="${CODEX_INSTALL_SOURCE:-}"
if [[ -z "${codex_source}" ]]; then
  codex_source="$(command -v codex 2>/dev/null || true)"
fi
if [[ -z "${codex_source}" && -x "${CODEX_DST}" ]]; then
  codex_source="${CODEX_DST}"
fi
if [[ -z "${codex_source}" ]]; then
  for candidate in /home/*/.local/bin/codex /home/*/.npm-global/bin/codex; do
    if [[ -x "${candidate}" ]]; then
      codex_source="${candidate}"
      break
    fi
  done
fi
if [[ -z "${codex_source}" ]]; then
  install_codex_cli
  codex_source="$(command -v codex 2>/dev/null || true)"
fi
if [[ -z "${codex_source}" ]]; then
  echo "Codex CLI installation completed but codex was not found in PATH." >&2
  exit 1
fi
codex_real="$(readlink -f "${codex_source}")"
if [[ "${codex_real}" != "${CODEX_DST}" ]]; then
  if [[ "${codex_real}" == /home/* ]]; then
    install_executable_atomic "${codex_real}" "${CODEX_DST}"
  else
    codex_link_dir="$(mktemp -d /usr/local/bin/.codex-link.XXXXXX)"
    ln -s "${codex_real}" "${codex_link_dir}/codex"
    mv -Tf "${codex_link_dir}/codex" "${CODEX_DST}"
    rmdir "${codex_link_dir}"
  fi
fi

if [[ -d "/home/${SERVICE_USER}/.codex" || -z "$(find "${APP_DIR}/.codex" -mindepth 1 -print -quit)" ]]; then
  bash "${ROOT_DIR}/scripts/migrate-codex-config.sh"
fi

run_as_service() {
  runuser -u "${SERVICE_USER}" -- env \
    HOME="${APP_DIR}" \
    PATH=/usr/local/bin:/usr/bin:/bin \
    "$@"
}

if ! run_as_service "${CODEX_DST}" --version; then
  echo "Codex CLI cannot run as ${SERVICE_USER} from ${CODEX_DST}. Install a standalone executable and rerun." >&2
  exit 1
fi
codex_help="$(run_as_service "${CODEX_DST}" exec --help)"
if ! grep -q -- '--image' <<<"${codex_help}"; then
  echo "Installed Codex CLI does not support 'codex exec --image'; upgrade Codex CLI and rerun." >&2
  exit 1
fi

systemctl daemon-reload
if [[ "${ENABLE_SERVICE}" == true ]]; then
  systemctl enable "${SERVICE_NAME}"
fi
if [[ "${START_SERVICE}" == true ]]; then
  systemctl restart "${SERVICE_NAME}"
fi

echo "Installation complete."
echo "Edit ${ETC_DIR}/codex-agent.env, migrate/authenticate Codex if needed, then run:"
echo "  systemctl enable --now ${SERVICE_NAME}"
echo "Administrative menu:"
echo "  llm-codex"
