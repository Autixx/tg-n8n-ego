#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_DIR="/opt/codex-agent"
ETC_DIR="/etc/codex-agent"
BIN_SRC="${ROOT_DIR}/bin/codex-agent"
BIN_DST="/usr/local/bin/codex-agent"

if [[ "${EUID}" -ne 0 ]]; then
  echo "install.sh must be run as root" >&2
  exit 1
fi

if ! id -u codexagent >/dev/null 2>&1; then
  useradd --system --home-dir "${APP_DIR}" --shell /usr/sbin/nologin codexagent
fi

install -d -m 0750 -o codexagent -g codexagent "${APP_DIR}"
install -d -m 0750 -o codexagent -g codexagent "${APP_DIR}/jobs" "${APP_DIR}/logs" "${APP_DIR}/prompts" "${APP_DIR}/schemas"
install -d -m 0750 "${ETC_DIR}"

cp -a "${ROOT_DIR}/prompts/." "${APP_DIR}/prompts/"
cp -a "${ROOT_DIR}/schemas/." "${APP_DIR}/schemas/"
chown -R codexagent:codexagent "${APP_DIR}"

install -m 0640 "${ROOT_DIR}/configs/codex-agent.env.example" "${ETC_DIR}/codex-agent.env.example"
if [[ ! -f "${ETC_DIR}/codex-agent.env" ]]; then
  echo "Created ${ETC_DIR}/codex-agent.env.example. Copy it to ${ETC_DIR}/codex-agent.env and set CODEX_AGENT_TOKEN."
fi

if [[ -f "${BIN_SRC}" ]]; then
  install -m 0755 "${BIN_SRC}" "${BIN_DST}"
else
  echo "Binary ${BIN_SRC} not found; run scripts/build.sh before installing the service binary." >&2
fi

install -m 0644 "${ROOT_DIR}/configs/codex-agent.service" /etc/systemd/system/codex-agent.service
systemctl daemon-reload

echo "Install complete. Configure ${ETC_DIR}/codex-agent.env, then run:"
echo "  systemctl enable --now codex-agent"
