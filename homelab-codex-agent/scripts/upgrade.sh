#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVICE_NAME="codex-agent.service"

if [[ "${EUID}" -ne 0 ]]; then
  if command -v sudo >/dev/null 2>&1; then
    echo "upgrade.sh requires root; re-running with sudo." >&2
    exec sudo -- bash "${BASH_SOURCE[0]}" "$@"
  fi
  echo "upgrade.sh requires root." >&2
  exit 1
fi

if ! command -v go >/dev/null 2>&1 && [[ -x /usr/local/go/bin/go ]]; then
  export PATH="/usr/local/go/bin:${PATH}"
fi

if ! command -v go >/dev/null 2>&1; then
  echo "Go 1.22 or newer is required for upgrade; install Go and rerun." >&2
  exit 1
fi

if ! command -v whiptail >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y --no-install-recommends whiptail
fi

cd "${ROOT_DIR}"
go test ./...
bash ./scripts/build.sh

service_was_active=false
if systemctl is-active --quiet "${SERVICE_NAME}"; then
  service_was_active=true
  systemctl stop "${SERVICE_NAME}"
fi

restore_service_on_error() {
  if [[ "${service_was_active}" == true ]] && ! systemctl is-active --quiet "${SERVICE_NAME}"; then
    echo "Upgrade failed; attempting to restart the previously active service." >&2
    systemctl restart "${SERVICE_NAME}" || true
  fi
}
trap restore_service_on_error ERR

bash ./scripts/install.sh --skip-packages --skip-build
systemctl restart "${SERVICE_NAME}"
bash ./scripts/doctor.sh
curl --fail --silent --show-error --max-time 5 http://127.0.0.1:19090/healthz | jq .
trap - ERR

echo "Upgrade complete. Existing /etc/codex-agent/codex-agent.env was preserved."
