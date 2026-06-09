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
