#!/usr/bin/env bash
set -euo pipefail

SERVICE_NAME="${CODEX_AGENT_SERVICE_NAME:-codex-agent.service}"
SERVICE_USER="${CODEX_AGENT_SERVICE_USER:-codexagent}"
APP_DIR="${CODEX_AGENT_APP_DIR:-/opt/codex-agent}"
ENV_FILE="${CODEX_AGENT_ENV_FILE:-/etc/codex-agent/codex-agent.env}"
DEFAULT_REPO_DIR="/opt/src/codex-agent"

if [[ -n "${CODEX_AGENT_REPO_DIR:-}" ]]; then
  REPO_DIR="${CODEX_AGENT_REPO_DIR}"
elif [[ -d "${DEFAULT_REPO_DIR}/.git" ]]; then
  REPO_DIR="${DEFAULT_REPO_DIR}"
else
  REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fi

if ! command -v whiptail >/dev/null 2>&1; then
  echo "whiptail is required for llm-codex. Install it with: sudo apt-get install whiptail" >&2
  exit 1
fi

TITLE="llm-codex admin"
HEIGHT=24
WIDTH=78

sudo_cmd() {
  if [[ "${EUID}" -eq 0 ]]; then
    "$@"
  else
    sudo "$@"
  fi
}

ensure_sudo() {
  if [[ "${EUID}" -ne 0 ]]; then
    sudo -v
  fi
}

run_as_service() {
  if [[ "${EUID}" -eq 0 ]]; then
    runuser -u "${SERVICE_USER}" -- env HOME="${APP_DIR}" PATH=/usr/local/bin:/usr/bin:/bin "$@"
  else
    sudo -u "${SERVICE_USER}" -- env HOME="${APP_DIR}" PATH=/usr/local/bin:/usr/bin:/bin "$@"
  fi
}

message() {
  whiptail --title "${TITLE}" --msgbox "$1" "${HEIGHT}" "${WIDTH}"
}

show_file() {
  local heading="$1"
  local file="$2"
  whiptail --title "${heading}" --textbox "${file}" "${HEIGHT}" "${WIDTH}"
}

run_logged() {
  local heading="$1"
  shift
  local log_file
  log_file="$(mktemp)"
  {
    printf '$'
    printf ' %q' "$@"
    printf '\n\n'
    "$@"
  } >"${log_file}" 2>&1
  local status=$?
  if [[ "${status}" -eq 0 ]]; then
    printf '\nOK\n' >>"${log_file}"
  else
    printf '\nFAILED: exit code %d\n' "${status}" >>"${log_file}"
  fi
  show_file "${heading}" "${log_file}"
  rm -f "${log_file}"
  return "${status}"
}

env_value() {
  local key="$1"
  if [[ -r "${ENV_FILE}" ]]; then
    awk -F= -v key="${key}" '
      $1 == key {
        value = substr($0, index($0, "=") + 1)
        gsub(/^"|"$/, "", value)
        gsub(/^'\''|'\''$/, "", value)
        print value
        exit
      }
    ' "${ENV_FILE}"
  fi
}

health_url() {
  local listen
  listen="$(env_value CODEX_AGENT_LISTEN)"
  listen="${listen:-127.0.0.1:19090}"
  listen="${listen#http://}"
  listen="${listen#https://}"
  if [[ "${listen}" == :* ]]; then
    listen="127.0.0.1${listen}"
  elif [[ "${listen}" == 0.0.0.0:* || "${listen}" == "[::]:"* ]]; then
    listen="127.0.0.1:${listen##*:}"
  fi
  printf 'http://%s/healthz' "${listen}"
}

check_health() {
  local url log_file status
  url="$(health_url)"
  log_file="$(mktemp)"
  (
    printf 'Service: %s\n' "${SERVICE_NAME}"
    printf 'Health URL: %s\n\n' "${url}"
    systemctl --no-pager --full status "${SERVICE_NAME}" || true
    printf '\n--- /healthz ---\n'
    curl --fail --silent --show-error --max-time 5 "${url}"
    status=$?
    printf '\n'
    exit "${status}"
  ) >"${log_file}" 2>&1
  status=$?
  if [[ "${status}" -eq 0 ]]; then
    printf '\nHealth check OK\n' >>"${log_file}"
  else
    printf '\nHealth check FAILED: exit code %d\n' "${status}" >>"${log_file}"
  fi
  show_file "Health" "${log_file}"
  rm -f "${log_file}"
}

check_codex_cli() {
  local log_file status
  ensure_sudo
  log_file="$(mktemp)"
  (
    printf 'Codex binary: /usr/local/bin/codex\n'
    printf 'Service user: %s\n\n' "${SERVICE_USER}"
    run_as_service /usr/local/bin/codex --version
    printf '\n--- codex exec --help image support ---\n'
    if run_as_service /usr/local/bin/codex exec --help | grep -q -- '--image'; then
      printf 'codex exec supports --image\n'
    else
      printf 'codex exec does not expose --image\n' >&2
      exit 1
    fi
  ) >"${log_file}" 2>&1
  status=$?
  if [[ "${status}" -eq 0 ]]; then
    printf '\nCodex CLI check OK\n' >>"${log_file}"
  else
    printf '\nCodex CLI check FAILED: exit code %d\n' "${status}" >>"${log_file}"
  fi
  show_file "Codex CLI" "${log_file}"
  rm -f "${log_file}"
}

update_from_github() {
  local log_file state_file upstream ahead behind status
  if [[ ! -d "${REPO_DIR}/.git" ]]; then
    message "Git repository was not found at ${REPO_DIR}."
    return
  fi

  log_file="$(mktemp)"
  state_file="$(mktemp)"
  (
    cd "${REPO_DIR}"
    printf 'Repository: %s\n' "${REPO_DIR}"
    printf 'Remote origin: '
    git remote get-url origin
    printf '\n'
    git fetch --prune origin
    upstream="$(git rev-parse --abbrev-ref --symbolic-full-name '@{u}' 2>/dev/null || true)"
    if [[ -z "${upstream}" ]]; then
      printf 'Current branch has no upstream. Configure tracking before using admin updates.\n' >&2
      exit 2
    fi
    read -r ahead behind < <(git rev-list --left-right --count "HEAD...${upstream}")
    printf 'Upstream: %s\n' "${upstream}"
    printf 'Local ahead: %s\n' "${ahead}"
    printf 'Remote updates: %s\n' "${behind}"
    {
      printf 'upstream=%q\n' "${upstream}"
      printf 'ahead=%q\n' "${ahead}"
      printf 'behind=%q\n' "${behind}"
    } >"${state_file}"
  ) >"${log_file}" 2>&1
  status=$?
  if [[ "${status}" -ne 0 ]]; then
    printf '\nUpdate check FAILED: exit code %d\n' "${status}" >>"${log_file}"
    show_file "GitHub Updates" "${log_file}"
    rm -f "${log_file}"
    rm -f "${state_file}"
    return
  fi
  # shellcheck disable=SC1090
  source "${state_file}"

  show_file "GitHub Updates" "${log_file}"
  rm -f "${log_file}"
  rm -f "${state_file}"

  if [[ "${behind}" -eq 0 ]]; then
    message "No updates found. Repository is up to date with ${upstream}."
    return
  fi

  if [[ "${ahead}" -gt 0 ]]; then
    message "Updates are available, but the local branch also has ${ahead} unpushed commit(s). Resolve this manually in ${REPO_DIR}."
    return
  fi

  if whiptail --title "${TITLE}" --yesno "Found ${behind} update(s) on ${upstream}. Pull from GitHub and run upgrade now?" 10 "${WIDTH}"; then
    ensure_sudo
    (
      cd "${REPO_DIR}"
      run_logged "GitHub Upgrade" bash -lc "git pull --ff-only && sudo ./scripts/upgrade.sh"
    ) || true
  fi
}

stop_service() {
  if whiptail --title "${TITLE}" --yesno "Stop ${SERVICE_NAME}?" 8 "${WIDTH}"; then
    ensure_sudo
    run_logged "Stop Service" sudo_cmd systemctl stop "${SERVICE_NAME}" || true
  fi
}

restart_service() {
  if whiptail --title "${TITLE}" --yesno "Restart ${SERVICE_NAME} now?" 8 "${WIDTH}"; then
    ensure_sudo
    run_logged "Restart Service" sudo_cmd systemctl restart "${SERVICE_NAME}" || true
  fi
}

toggle_autostart() {
  local state action
  if systemctl is-enabled --quiet "${SERVICE_NAME}" 2>/dev/null; then
    state="enabled"
    action="disable"
  else
    state="disabled"
    action="enable"
  fi
  if whiptail --title "${TITLE}" --yesno "Autostart is currently ${state}. Do you want to ${action} it?" 9 "${WIDTH}"; then
    ensure_sudo
    run_logged "Autostart" sudo_cmd systemctl "${action}" "${SERVICE_NAME}" || true
  fi
}

while true; do
  choice="$(
    whiptail --title "${TITLE}" --menu "Service: ${SERVICE_NAME}" "${HEIGHT}" "${WIDTH}" 12 \
      "1" "Check health" \
      "2" "Check GitHub updates and upgrade" \
      "3" "Check Codex CLI" \
      "4" "Stop service" \
      "5" "Restart service" \
      "6" "Toggle service autostart" \
      "7" "Exit" \
      3>&1 1>&2 2>&3
  )" || exit 0

  case "${choice}" in
    1) check_health ;;
    2) update_from_github ;;
    3) check_codex_cli ;;
    4) stop_service ;;
    5) restart_service ;;
    6) toggle_autostart ;;
    7) exit 0 ;;
  esac
done
