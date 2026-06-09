#!/usr/bin/env bash
set -euo pipefail
mkdir -p bin
go build -o ./bin/codex-agent ./cmd/codex-agent
