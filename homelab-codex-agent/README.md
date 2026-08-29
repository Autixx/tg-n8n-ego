# Homelab Codex Agent

Small local HTTP daemon for Debian 13 VPS hosts. It accepts requests from n8n or another local automation tool, writes a job directory, runs a fixed `codex exec` command, then returns `result.json` and `eventlog.jsonl` as JSON.

The service is designed for loopback, VPN, or reverse tunnel use only. Do not expose it directly to the public internet.

## Runtime Layout

```text
/opt/codex-agent/
  .codex/
  codex-sessions.json
  jobs/
  logs/
  prompts/
  schemas/
  projects/
  templates/
  test/
  attachment-registry.xml
```

The service runs as `codexagent` with `HOME=/opt/codex-agent`. Codex authentication and configuration therefore live in `/opt/codex-agent/.codex`, not `/home/codexagent`.

## Fresh Install On Debian 13

Go 1.22 or newer and Git are needed to clone and build the project. Install Go using the official instructions at <https://go.dev/doc/install> and verify:

```bash
go version
```

Clone the repository:

```bash
sudo mkdir -p /opt/src
sudo git clone https://github.com/Autixx/tg-n8n-ego.git /opt/src/codex-agent
cd /opt/src/codex-agent
```

Install all Debian runtime packages, run tests, build the agent, prepare runtime directories, verify or install Codex CLI, and install the systemd unit:

```bash
sudo ./scripts/install.sh
sudo editor /etc/codex-agent/codex-agent.env
sudo ./scripts/migrate-codex-config.sh
sudo systemctl enable --now codex-agent
./scripts/doctor.sh
curl -sS http://127.0.0.1:19090/healthz | jq .
```

The repository-root scripts delegate to the implementation in `homelab-codex-agent/scripts/`, so the same commands also work when run directly from the package directory.

The installer never overwrites an existing `/etc/codex-agent/codex-agent.env`. On first install it creates the file from the example. Replace both placeholder secrets before starting the service.

By default, installation does not enable or start the service. Explicit alternatives are:

```bash
sudo ./scripts/install.sh --enable  # enable at boot, do not start
sudo ./scripts/install.sh --start   # enable and start/restart
```

The installer uses an existing Codex CLI when available. If Codex is missing, it installs `@openai/codex` with npm, then verifies as `codexagent` that `codex --version`, `codex exec --help`, and image support work with the service HOME and PATH. An incompatible Codex CLI stops installation with an actionable error.

## Upgrade

From the existing checkout:

```bash
cd /opt/src/codex-agent
git pull --ff-only
sudo ./scripts/upgrade.sh
```

The upgrade script tests and builds before stopping the service, preserves the real env file, updates the binary/resources/unit, restarts the service, runs doctor, and checks `/healthz`. If upgrade fails after stopping an active service, it attempts to restart it.

## Doctor

Run diagnostics at any time:

```bash
./scripts/doctor.sh
```

Doctor verifies installed binaries, service user access, runtime resources, Codex execution and `--image` support, bubblewrap, systemd installation, and the health endpoint when the service is active.

## Admin Menu

The installer adds an interactive terminal menu:

```bash
llm-codex
```

The menu checks `/healthz`, runs a doctor report for binaries, Codex config, prompts, schemas, systemd, health, and image-capable `codex exec`, checks GitHub for repository updates and can run the normal upgrade flow, verifies Codex CLI availability, stops or restarts the service, toggles systemd autostart, logs in to Codex through either API key or ChatGPT browser/device auth, and edits prompt files under `/opt/codex-agent/prompts`. It uses `whiptail`, so it opens as a full terminal dialog instead of clearing and repainting the shell.

When selecting a prompt in `Edit prompts`, `OK` writes it to `CODEX_AGENT_PROMPT` in `/etc/codex-agent/codex-agent.env`; `Edit` opens the file with `nano` and then selects it. New prompt files are created in `/opt/codex-agent/prompts` and appear in the same menu. Restart the service after changing the active prompt.

## OS Reinstall Backup

Back up these files before reinstalling the VPS:

- `/etc/codex-agent/codex-agent.env`
- `/opt/codex-agent/.codex/`
- `/home/codexagent/.codex/` if it still exists
- manually edited `/opt/codex-agent/prompts/`, `/opt/codex-agent/schemas/`, and `/opt/codex-agent/projects/`
- `/home/tunnel/.ssh/authorized_keys`
- SSH daemon configuration when `PermitListen` is configured outside `authorized_keys`

## Build

```bash
./scripts/build.sh
```

Equivalent command:

```bash
go build -o ./bin/codex-agent ./cmd/codex-agent
```

## Configure

Set a long random token:

```bash
CODEX_AGENT_TOKEN=CHANGE_ME_LONG_RANDOM_SECRET
```

For Dashboard attachment downloads, set a different service token and attachment limits:

```bash
CODEX_AGENT_DASHBOARD_ATTACHMENT_TOKEN=CHANGE_ME_SEPARATE_DASHBOARD_SERVICE_SECRET
CODEX_AGENT_MAX_ATTACHMENTS=4
CODEX_AGENT_MAX_ATTACHMENT_BYTES=10485760
CODEX_AGENT_ALLOW_IMAGE_ATTACHMENTS=true
CODEX_AGENT_MULTIMODAL_MODE=auto
CODEX_AGENT_ATTACHMENT_REGISTRY=/opt/codex-agent/attachment-registry.xml
CODEX_AGENT_ATTACHMENT_RETENTION_HOURS=24
CODEX_AGENT_CLEANUP_INTERVAL_MINUTES=60

CODEX_AGENT_SESSION_ENABLED=true
CODEX_AGENT_SESSION_MAX_TURNS=10
CODEX_AGENT_SESSION_MAX_AGE_MINUTES=360
CODEX_AGENT_SESSION_PURPOSE=projectego-decompose
CODEX_AGENT_SESSION_STORE_PATH=/opt/codex-agent/codex-sessions.json
CODEX_AGENT_RUNNER=exec
CODEX_AGENT_RUNNER_FALLBACK=exec
CODEX_AGENT_APP_SERVER_URL=unix:///opt/codex-agent/codex-app-server.sock
CODEX_AGENT_OUTCOME_WEBHOOK_URL=
CODEX_AGENT_OUTCOME_WEBHOOK_TIMEOUT_SECONDS=10
```

`CODEX_AGENT_MULTIMODAL_MODE` accepts `auto`, `enabled`, or `disabled`. Both `auto` and `enabled` verify that the installed `codex exec` exposes `--image`; attachment requests fail explicitly when the capability is unavailable. Text-only requests do not perform this capability check.

## API

Health check:

```bash
curl -sS http://127.0.0.1:19090/healthz | jq .
```

### ProjectEGO V2 Decomposition

The v2 endpoint decomposes ProjectEGO work items with `domain_hint` and `module_hint`. It does not emit Plane projects, PM board names, PM UUIDs, or n8n routing decisions.

V2 keeps short-lived session metadata in `/opt/codex-agent/codex-sessions.json`. With the default `CODEX_AGENT_RUNNER=exec`, the service uses the stable fallback path: it stores turn history and summaries, sends bootstrap + latest summary + current compact request, and returns the warning `codex_session_resume_unavailable`.

V2 supports four modes: `advisor`, `structured_breakdown`, `create_tasks`, and `abstract_idea`. `advisor` returns a structured answer in `result.answer_markdown` for planning/research questions and does not create draft task items as the primary output. The other modes continue to return `result.items` for Dashboard review and manual apply/keep/drop decisions.

Set `CODEX_AGENT_RUNNER=appserver` to use Codex App Server threads for v2. In this mode the agent connects to the long-running `codex-app-server.service` through `codex app-server proxy`, creates or resumes a persisted Codex thread, sends the bootstrap prompt only when the Codex thread is created, and sends compact per-message input on later turns. If app-server fails and `CODEX_AGENT_RUNNER_FALLBACK=exec`, v2 returns `codex_appserver_unavailable` and falls back to `codex exec`.

`CODEX_AGENT_SESSION_MAX_TURNS` controls how many successful v2 turns are stored in one ProjectEGO session before rotation. When the current session reaches the limit, the agent closes it, saves a compact summary, creates a new session for the same `thread_id`, and sends that summary with the next request. This is the main knob for forcing a fresh Codex App Server thread after a fixed number of messages.

Before enabling app-server mode, verify Codex CLI support and auth as the service user:

```bash
sudo -u codexagent HOME=/opt/codex-agent /usr/local/bin/codex app-server --help
sudo -u codexagent HOME=/opt/codex-agent /usr/local/bin/codex login status
```

Enable the production app-server path:

```bash
sudo systemctl enable --now codex-app-server
sudo editor /etc/codex-agent/codex-agent.env
sudo systemctl restart codex-agent
llm-codex
```

Set these values in `/etc/codex-agent/codex-agent.env`:

```env
CODEX_AGENT_RUNNER=appserver
CODEX_AGENT_RUNNER_FALLBACK=exec
CODEX_AGENT_APP_SERVER_URL=unix:///opt/codex-agent/codex-app-server.sock
CODEX_AGENT_SESSION_MAX_TURNS=10
```

Set `CODEX_AGENT_OUTCOME_WEBHOOK_URL` to receive best-effort POST notifications for accepted v1/v2 jobs. Webhook delivery failures are logged but do not change the normal API response. The payload includes `status`, `endpoint`, `job_id`, session/thread IDs when available, `runner`, `primary_runner`, `fallback_runner`, `fallback_used`, warnings/error, attachment counts, and duration. Use this to see app-server failures and exec fallback events outside the HTTP response path.

Set `CODEX_AGENT_SESSION_ENABLED=false` to run v2 without writing session state. The API response remains stable and includes `session_manager_disabled`.

`client_request_id` is an optional v2 per-request correlation ID generated by the client before sending the request. Dashboard should generate it for every submitted request to correlate Dashboard UI request -> codex-agent `job_id` -> `session.id` / `session.codex_session_id` -> later n8n or PM workflow. `thread_id` is not a request ID; it is a long-lived semantic session key.

Advisor smoke test:

```bash
curl -sS -X POST \
  http://127.0.0.1:19090/v2/projectego/decompose \
  -H "Content-Type: application/json" \
  -H "X-Codex-Agent-Token: $CODEX_AGENT_TOKEN" \
  -d '{
    "client_request_id": "manual_test_001",
    "thread_id": "projectego-intake",
    "mode": "advisor",
    "source": "manual-test",
    "text": "Need to start a UE5 game project. Explain a practical project creation pipeline, recommend the order for base mechanics, and suggest concise learning resources."
  }' | jq .
```

Attachment metadata smoke test:

```bash
curl -sS -X POST \
  http://127.0.0.1:19090/v2/projectego/decompose \
  -H "Content-Type: application/json" \
  -H "X-Codex-Agent-Token: $CODEX_AGENT_TOKEN" \
  -d '{
    "client_request_id": "manual_attachment_test_001",
    "thread_id": "projectego-intake",
    "mode": "structured_breakdown",
    "source": "manual-attachment-test",
    "text": "Analyze the image as a visual reference for UI/feedback tasks if the attachment is available.",
    "attachments": [
      {
        "id": "test-image-1",
        "kind": "image",
        "fileName": "reference.png",
        "mimeType": "image/png",
        "sizeBytes": 123456,
        "downloadUrl": "http://127.0.0.1:19100/api/internal/attachments/test-image-1"
      }
    ]
  }' | jq .
```

V2 validates `result.json` in-process. Results using only legacy `project` / `module` fields are rejected; `domain_hint` and `module_hint` are required.

V2 accepts attachment metadata with `kind: "file"` but does not extract text from non-image files yet. If text is present, the request continues text-only with `file_text_extraction_unavailable`; if only unsupported files are present, v2 returns a clear warning error instead of inventing content.

### ProjectEGO V1 Classification

ProjectEGO processing request:

```bash
curl -sS -X POST http://127.0.0.1:19090/v1/projectego/process \
  -H "Content-Type: application/json" \
  -H "X-Codex-Agent-Token: $CODEX_AGENT_TOKEN" \
  -d '{
    "mode": "structured_breakdown",
    "source": "manual-test",
    "text": "Мне нужна система движения для игрока и врагов, связанная с Horde Framework, чтобы 50 врагов не считали сложную навигацию каждый кадр."
  }' | jq .
```

### Image Attachments

Dashboard stores uploaded files and sends metadata plus a secure internal download URL. The agent does not accept multipart uploads and does not base64-inline files.

```json
{
  "mode": "structured_breakdown",
  "source": "dashboard-upload",
  "text": "Analyze attached UI sketch and break it into ProjectEGO Dashboard tasks.",
  "fileName": "ui-sketch.png",
  "attachments": [
    {
      "id": "ATT_xxx",
      "kind": "image",
      "fileName": "ui-sketch.png",
      "mimeType": "image/png",
      "sizeBytes": 483920,
      "downloadUrl": "http://127.0.0.1:19100/api/internal/attachments/ATT_xxx"
    }
  ]
}
```

Supported image MIME types are `image/png`, `image/jpeg`, `image/svg+xml`, and `image/webp`. MIME type and filename extension must agree. Attachment URLs are restricted to HTTP(S) loopback targets, redirects are rejected, downloads use the separate Dashboard token as `Authorization: Bearer <token>`, and downloaded bytes are limited independently of the declared size.

Each attachment job contains:

```text
jobs/<job_id>/
  attachments/<safe_filename>
  attachments.json
  input.md
  eventlog.jsonl
```

### Attachment Retention

Every successfully staged attachment is recorded in an XML registry. Registry paths are relative to `CODEX_AGENT_WORKDIR` and are validated again before deletion.

```text
/opt/codex-agent/attachment-registry.xml
```

The cleanup scheduler runs once when the daemon starts and then every `CODEX_AGENT_CLEANUP_INTERVAL_MINUTES`. Files whose recorded staging time is at least `CODEX_AGENT_ATTACHMENT_RETENTION_HOURS` old are deleted together with their XML registry entries. Defaults are 24 hours retention and a 60 minute cleanup interval.

Only files under job `attachments/` that were registered by the agent are removed. `input.md`, `result.json`, `eventlog.jsonl`, status, and logs remain available through the existing job API. After the last file is removed, the empty `attachments/` directory is removed when possible.

Changing retention settings requires restarting the service:

```bash
sudo editor /etc/codex-agent/codex-agent.env
sudo systemctl restart codex-agent
```

The installed Codex CLI must support `codex exec --image`. Otherwise the request returns HTTP 500 with `image_attachments_not_supported_by_current_codex_cli`; image attachments are never silently ignored.

### Dashboard Reverse SSH

The Dashboard attachment endpoint must be reachable from the VPS/codex-agent. For the current deployment, expose it only on VPS loopback through reverse SSH:

```bash
-R 127.0.0.1:19100:192.168.1.237:19100
```

Dashboard should then generate internal URLs in this form:

```text
http://127.0.0.1:19100/api/internal/attachments/<attachmentId>
```

Fetch job files:

```bash
curl -sS -H "X-Codex-Agent-Token: $CODEX_AGENT_TOKEN" \
  http://127.0.0.1:19090/v1/jobs/<job_id> | jq .

curl -sS -H "X-Codex-Agent-Token: $CODEX_AGENT_TOKEN" \
  http://127.0.0.1:19090/v1/jobs/<job_id>/result | jq .

curl -sS -H "X-Codex-Agent-Token: $CODEX_AGENT_TOKEN" \
  http://127.0.0.1:19090/v1/jobs/<job_id>/eventlog | jq .
```

## Security Assumptions

- The default listener is `127.0.0.1:19090`.
- All non-health API routes require `X-Codex-Agent-Token`.
- Token comparison uses constant-time comparison.
- The daemon refuses to start when `CODEX_AGENT_TOKEN` is empty.
- `job_id` accepts only `^[a-zA-Z0-9._-]+$` and is never used to run commands.
- The Codex command is fixed by the server and cannot be supplied by HTTP clients.
- Input text is written to job files, but is not logged whole to system logs.
- Attachment download authentication uses a separate service token; Dashboard user cookies are not forwarded or required.
- Attachment URLs are limited to loopback HTTP(S), redirects are disabled, filenames are sanitized, and files are created without overwrite inside the job directory.
