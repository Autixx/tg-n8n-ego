# Homelab Codex Agent

Small local HTTP daemon for Debian 13 VPS hosts. It accepts requests from n8n or another local automation tool, writes a job directory, runs a fixed `codex exec` command, then returns `result.json` and `eventlog.jsonl` as JSON.

The service is designed for loopback, VPN, or reverse tunnel use only. Do not expose it directly to the public internet.

## Runtime Layout

```text
/opt/codex-agent/
  jobs/
  prompts/
  schemas/
  logs/
```

## Install Go On Debian 13

```bash
sudo apt update
sudo apt install -y wget tar ca-certificates
wget https://go.dev/dl/go1.22.12.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.22.12.linux-amd64.tar.gz
echo 'export PATH=/usr/local/go/bin:$PATH' >> ~/.profile
. ~/.profile
go version
```

Go 1.22 or newer is required.

## Build

```bash
./scripts/build.sh
```

Equivalent command:

```bash
go build -o ./bin/codex-agent ./cmd/codex-agent
```

## Configure

Create `/etc/codex-agent/codex-agent.env` from the example:

```bash
sudo mkdir -p /etc/codex-agent
sudo cp configs/codex-agent.env.example /etc/codex-agent/codex-agent.env
sudo chmod 0640 /etc/codex-agent/codex-agent.env
sudo editor /etc/codex-agent/codex-agent.env
```

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
```

`CODEX_AGENT_MULTIMODAL_MODE` accepts `auto`, `enabled`, or `disabled`. Both `auto` and `enabled` verify that the installed `codex exec` exposes `--image`; attachment requests fail explicitly when the capability is unavailable. Text-only requests do not perform this capability check.

## Install Systemd Unit

```bash
sudo ./scripts/install.sh
sudo systemctl enable --now codex-agent
sudo systemctl status codex-agent
```

The installer creates the `codexagent` user, `/opt/codex-agent`, `/etc/codex-agent`, copies prompts and schemas, installs `./bin/codex-agent` when present, copies the systemd unit, and runs `systemctl daemon-reload`.

## API

Health check:

```bash
curl -sS http://127.0.0.1:19090/healthz | jq .
```

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
