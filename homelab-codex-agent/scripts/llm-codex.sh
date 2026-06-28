#!/usr/bin/env bash
set -euo pipefail

SERVICE_NAME="${CODEX_AGENT_SERVICE_NAME:-codex-agent.service}"
SERVICE_USER="${CODEX_AGENT_SERVICE_USER:-codexagent}"
APP_DIR="${CODEX_AGENT_APP_DIR:-/opt/codex-agent}"
ENV_FILE="${CODEX_AGENT_ENV_FILE:-/etc/codex-agent/codex-agent.env}"
PROMPTS_DIR="${CODEX_AGENT_PROMPTS_DIR:-${APP_DIR}/prompts}"
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

update_env_value() {
  local key="$1"
  local value="$2"
  local tmp
  tmp="$(mktemp)"
  if [[ -f "${ENV_FILE}" ]]; then
    sudo_cmd awk -v key="${key}" -v value="${value}" '
      BEGIN { replaced = 0 }
      $0 ~ "^" key "=" {
        print key "=" value
        replaced = 1
        next
      }
      { print }
      END {
        if (replaced == 0) {
          print key "=" value
        }
      }
    ' "${ENV_FILE}" >"${tmp}"
  else
    printf '%s=%s\n' "${key}" "${value}" >"${tmp}"
  fi
  sudo_cmd install -m 0640 -o root -g "${SERVICE_USER}" "${tmp}" "${ENV_FILE}"
  rm -f "${tmp}"
}

env_value() {
  local key="$1"
  local reader=()
  if [[ -r "${ENV_FILE}" ]]; then
    reader=(awk)
  elif [[ -f "${ENV_FILE}" ]] && sudo -n true 2>/dev/null; then
    reader=(sudo awk)
  else
    return 0
  fi
  "${reader[@]}" -F= -v key="${key}" '
      $1 == key {
        value = substr($0, index($0, "=") + 1)
        gsub(/^"|"$/, "", value)
        gsub(/^'\''|'\''$/, "", value)
        print value
        exit
      }
    ' "${ENV_FILE}"
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

login_with_api_key() {
  local api_key log_file status
  api_key="$(
    whiptail --title "${TITLE}" --passwordbox "Enter OpenAI API key for Codex CLI. It will be passed to 'codex login --with-api-key' as ${SERVICE_USER}." 10 "${WIDTH}" \
      3>&1 1>&2 2>&3
  )" || return
  if [[ -z "${api_key}" ]]; then
    message "API key was empty; login was skipped."
    return
  fi

  ensure_sudo
  log_file="$(mktemp)"
  (
    printf '%s\n' "${api_key}" | run_as_service /usr/local/bin/codex login --with-api-key
  ) >"${log_file}" 2>&1
  status=$?
  unset api_key
  if [[ "${status}" -eq 0 ]]; then
    printf '\nCodex API key login OK\n' >>"${log_file}"
  else
    printf '\nCodex API key login FAILED: exit code %d\n' "${status}" >>"${log_file}"
  fi
  show_file "Codex Login" "${log_file}"
  rm -f "${log_file}"
}

login_with_chatgpt() {
  ensure_sudo
  whiptail --title "${TITLE}" --msgbox "The terminal will now run 'codex login --device-auth' as ${SERVICE_USER}. Open the browser link shown by Codex, complete login, then return here." 10 "${WIDTH}"
  printf '\nRunning Codex browser/device login as %s.\n\n' "${SERVICE_USER}"
  run_as_service /usr/local/bin/codex login --device-auth || true
  printf '\nPress Enter to return to llm-codex...'
  read -r _
}

login_to_codex() {
  local choice
  choice="$(
    whiptail --title "${TITLE}" --menu "Login to Codex via" 14 "${WIDTH}" 6 \
      "1" "Codex API Key" \
      "2" "ChatGPT Auth" \
      "3" "Cancel" \
      3>&1 1>&2 2>&3
  )" || return
  case "${choice}" in
    1) login_with_api_key ;;
    2) login_with_chatgpt ;;
  esac
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

prompt_basename_is_valid() {
  local name="$1"
  [[ -n "${name}" && "${name}" != "." && "${name}" != ".." && "${name}" != *"/"* && "${name}" != *"\\"* ]]
}

set_active_prompt() {
  local prompt_path="$1"
  ensure_sudo
  update_env_value CODEX_AGENT_PROMPT "${prompt_path}"
  message "Active prompt set to:\n${prompt_path}"
  if whiptail --title "${TITLE}" --yesno "Restart ${SERVICE_NAME} now so the new prompt is used?" 9 "${WIDTH}"; then
    ensure_sudo
    run_logged "Restart Service" sudo_cmd systemctl restart "${SERVICE_NAME}" || true
  fi
}

edit_prompt_file() {
  local prompt_path="$1"
  ensure_sudo
  sudo_cmd nano "${prompt_path}"
  sudo_cmd chown "${SERVICE_USER}:${SERVICE_USER}" "${prompt_path}"
  sudo_cmd chmod 0640 "${prompt_path}"
}

create_prompt() {
  local empty_file name prompt_path
  name="$(
    whiptail --title "${TITLE}" --inputbox "New prompt file name" 10 "${WIDTH}" "new_prompt.md" \
      3>&1 1>&2 2>&3
  )" || return
  if ! prompt_basename_is_valid "${name}"; then
    message "Invalid file name. Use a plain file name without slashes."
    return
  fi

  prompt_path="${PROMPTS_DIR}/${name}"
  ensure_sudo
  sudo_cmd install -d -m 0750 -o "${SERVICE_USER}" -g "${SERVICE_USER}" "${PROMPTS_DIR}"
  if [[ ! -e "${prompt_path}" ]]; then
    empty_file="$(mktemp)"
    sudo_cmd install -m 0640 -o "${SERVICE_USER}" -g "${SERVICE_USER}" "${empty_file}" "${prompt_path}"
    rm -f "${empty_file}"
  elif ! whiptail --title "${TITLE}" --yesno "${prompt_path} already exists. Open it anyway?" 9 "${WIDTH}"; then
    return
  fi
  edit_prompt_file "${prompt_path}"
  set_active_prompt "${prompt_path}"
}

prompt_actions() {
  local prompt_path="$1"
  local prompt_name="$2"
  local action
  action="$(
    whiptail --title "${TITLE}" --menu "${prompt_name}" 14 "${WIDTH}" 6 \
      "OK" "Use this prompt" \
      "Edit" "Open with nano and use this prompt" \
      "Cancel" "Return" \
      3>&1 1>&2 2>&3
  )" || return
  case "${action}" in
    OK) set_active_prompt "${prompt_path}" ;;
    Edit)
      edit_prompt_file "${prompt_path}"
      set_active_prompt "${prompt_path}"
      ;;
  esac
}

edit_prompts() {
  local active_prompt entries_file selected tag label prompt_path prompt_name
  ensure_sudo
  active_prompt="$(env_value CODEX_AGENT_PROMPT)"
  entries_file="$(mktemp)"
  sudo_cmd install -d -m 0750 -o "${SERVICE_USER}" -g "${SERVICE_USER}" "${PROMPTS_DIR}"

  while IFS= read -r prompt_path; do
    prompt_name="$(basename "${prompt_path}")"
    if [[ "${prompt_path}" == "${active_prompt}" ]]; then
      label="[*] ${prompt_name}"
    else
      label="[ ] ${prompt_name}"
    fi
    printf '%s\t%s\n' "${prompt_path}" "${label}" >>"${entries_file}"
  done < <(find "${PROMPTS_DIR}" -maxdepth 1 -type f | sort)
  printf '%s\t%s\n' "__create__" "Create new prompt..." >>"${entries_file}"

  mapfile -t menu_args < <(awk -F '\t' '{ print $1 "\n" $2 }' "${entries_file}")
  rm -f "${entries_file}"

  selected="$(
    whiptail --title "${TITLE}" --menu "Edit prompts" "${HEIGHT}" "${WIDTH}" 14 \
      "${menu_args[@]}" \
      3>&1 1>&2 2>&3
  )" || return

  if [[ "${selected}" == "__create__" ]]; then
    create_prompt
  else
    tag="$(basename "${selected}")"
    prompt_actions "${selected}" "${tag}"
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
      "7" "Login to Codex via" \
      "8" "Edit prompts" \
      "9" "Exit" \
      3>&1 1>&2 2>&3
  )" || exit 0

  case "${choice}" in
    1) check_health ;;
    2) update_from_github ;;
    3) check_codex_cli ;;
    4) stop_service ;;
    5) restart_service ;;
    6) toggle_autostart ;;
    7) login_to_codex ;;
    8) edit_prompts ;;
    9) exit 0 ;;
  esac
done
