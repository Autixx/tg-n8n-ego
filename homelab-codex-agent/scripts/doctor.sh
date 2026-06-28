#!/usr/bin/env bash
set -uo pipefail

SERVICE_USER="codexagent"
APP_DIR="/opt/codex-agent"
ENV_FILE="/etc/codex-agent/codex-agent.env"
SERVICE_FILE="/etc/systemd/system/codex-agent.service"
failures=0
warnings=0

pass() { printf '[PASS] %s\n' "$*"; }
warn() { printf '[WARN] %s\n' "$*" >&2; warnings=$((warnings + 1)); }
fail() { printf '[FAIL] %s\n' "$*" >&2; failures=$((failures + 1)); }

if [[ "${EUID}" -eq 0 ]]; then
  pass "running as root"
else
  pass "running as $(id -un); sudo may be requested for service-user checks"
fi

for binary in /usr/local/bin/codex-agent /usr/local/bin/llm-codex /usr/local/bin/codex /usr/bin/bwrap /usr/bin/jq /usr/bin/curl /usr/bin/nano /usr/bin/whiptail; do
  if [[ -x "${binary}" ]]; then
    pass "binary available: ${binary}"
  else
    fail "missing executable: ${binary}"
  fi
done

if id -u "${SERVICE_USER}" >/dev/null 2>&1; then
  pass "service user exists: ${SERVICE_USER}"
else
  fail "service user does not exist: ${SERVICE_USER}"
fi

run_as_service() {
  if [[ "${EUID}" -eq 0 ]]; then
    runuser -u "${SERVICE_USER}" -- env HOME="${APP_DIR}" PATH=/usr/local/bin:/usr/bin:/bin "$@"
  elif command -v sudo >/dev/null 2>&1; then
    sudo -u "${SERVICE_USER}" -- env HOME="${APP_DIR}" PATH=/usr/local/bin:/usr/bin:/bin "$@"
  else
    return 127
  fi
}

if [[ -f "${ENV_FILE}" ]]; then
  if run_as_service test -r "${ENV_FILE}"; then
    pass "service can read ${ENV_FILE}"
  else
    fail "service cannot read ${ENV_FILE}"
  fi
else
  fail "missing ${ENV_FILE}"
fi

for resource in \
  "${APP_DIR}/prompts/projectego_router.md" \
  "${APP_DIR}/schemas/projectego-classification.schema.json"; do
  if [[ -f "${resource}" ]]; then
    pass "runtime resource exists: ${resource}"
  else
    fail "missing runtime resource: ${resource}"
  fi
done

if [[ -f "${APP_DIR}/.codex/config.toml" ]]; then
  pass "Codex config exists"
else
  warn "${APP_DIR}/.codex/config.toml is absent; defaults may still work, but verify authentication/configuration"
fi

if codex_version="$(run_as_service /usr/local/bin/codex --version 2>&1)"; then
  pass "Codex runs as ${SERVICE_USER}: ${codex_version}"
else
  fail "Codex cannot run as ${SERVICE_USER}: ${codex_version}"
fi

if codex_help="$(run_as_service /usr/local/bin/codex exec --help 2>&1)"; then
  if grep -q -- '--image' <<<"${codex_help}"; then
    pass "Codex exec supports --image"
  else
    fail "Codex exec does not expose --image"
  fi
else
  fail "Codex exec --help failed"
fi

if [[ -x /usr/bin/bwrap ]]; then
  pass "bubblewrap is available at /usr/bin/bwrap"
else
  fail "bubblewrap is unavailable at /usr/bin/bwrap"
fi

if [[ -f "${SERVICE_FILE}" ]]; then
  pass "systemd unit is installed"
else
  fail "missing systemd unit: ${SERVICE_FILE}"
fi

if systemctl is-active --quiet codex-agent.service 2>/dev/null; then
  pass "codex-agent.service is active"
  if /usr/bin/curl --fail --silent --show-error --max-time 5 http://127.0.0.1:19090/healthz >/dev/null; then
    pass "health endpoint responds"
  else
    fail "health endpoint did not respond"
  fi
else
  warn "codex-agent.service is not active; health check skipped"
fi

printf 'Doctor complete: failures=%d warnings=%d\n' "${failures}" "${warnings}"
[[ "${failures}" -eq 0 ]]
