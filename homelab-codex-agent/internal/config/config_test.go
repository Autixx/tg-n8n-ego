package config

import (
	"testing"
	"time"
)

func TestAttachmentDefaults(t *testing.T) {
	t.Setenv("CODEX_AGENT_TOKEN", "agent-token")
	t.Setenv("CODEX_AGENT_DASHBOARD_ATTACHMENT_TOKEN", "dashboard-token")
	t.Setenv("CODEX_AGENT_MAX_ATTACHMENTS", "")
	t.Setenv("CODEX_AGENT_MAX_ATTACHMENT_BYTES", "")
	t.Setenv("CODEX_AGENT_ALLOW_IMAGE_ATTACHMENTS", "")
	t.Setenv("CODEX_AGENT_MULTIMODAL_MODE", "")
	t.Setenv("CODEX_AGENT_ATTACHMENT_REGISTRY", "")
	t.Setenv("CODEX_AGENT_ATTACHMENT_RETENTION_HOURS", "")
	t.Setenv("CODEX_AGENT_CLEANUP_INTERVAL_MINUTES", "")
	t.Setenv("CODEX_AGENT_RUNNER", "")
	t.Setenv("CODEX_AGENT_RUNNER_FALLBACK", "")
	t.Setenv("CODEX_AGENT_APP_SERVER_URL", "")
	t.Setenv("CODEX_AGENT_OUTCOME_WEBHOOK_URL", "")
	t.Setenv("CODEX_AGENT_OUTCOME_WEBHOOK_TIMEOUT_SECONDS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxAttachments != 4 {
		t.Fatalf("MaxAttachments = %d", cfg.MaxAttachments)
	}
	if cfg.MaxAttachmentBytes != 10485760 {
		t.Fatalf("MaxAttachmentBytes = %d", cfg.MaxAttachmentBytes)
	}
	if !cfg.AllowImageAttachments {
		t.Fatal("AllowImageAttachments = false")
	}
	if cfg.MultimodalMode != "auto" {
		t.Fatalf("MultimodalMode = %q", cfg.MultimodalMode)
	}
	if cfg.AttachmentRetention != 24*time.Hour {
		t.Fatalf("AttachmentRetention = %s", cfg.AttachmentRetention)
	}
	if cfg.CleanupInterval != time.Hour {
		t.Fatalf("CleanupInterval = %s", cfg.CleanupInterval)
	}
	if cfg.RunnerBackend != "exec" {
		t.Fatalf("RunnerBackend = %q", cfg.RunnerBackend)
	}
	if cfg.RunnerFallback != "exec" {
		t.Fatalf("RunnerFallback = %q", cfg.RunnerFallback)
	}
	if cfg.AppServerSocketPath != "/opt/codex-agent/codex-app-server.sock" {
		t.Fatalf("AppServerSocketPath = %q", cfg.AppServerSocketPath)
	}
	if cfg.OutcomeWebhookTimeout != 10*time.Second {
		t.Fatalf("OutcomeWebhookTimeout = %s", cfg.OutcomeWebhookTimeout)
	}
}

func TestInvalidMultimodalMode(t *testing.T) {
	t.Setenv("CODEX_AGENT_TOKEN", "agent-token")
	t.Setenv("CODEX_AGENT_MULTIMODAL_MODE", "maybe")
	if _, err := Load(); err == nil {
		t.Fatal("Load() unexpectedly succeeded")
	}
}

func TestInvalidRunnerMode(t *testing.T) {
	t.Setenv("CODEX_AGENT_TOKEN", "agent-token")
	t.Setenv("CODEX_AGENT_RUNNER", "resume-cli")
	if _, err := Load(); err == nil {
		t.Fatal("Load() unexpectedly succeeded")
	}
}

func TestInvalidAppServerURL(t *testing.T) {
	t.Setenv("CODEX_AGENT_TOKEN", "agent-token")
	t.Setenv("CODEX_AGENT_APP_SERVER_URL", "ws://127.0.0.1:4500")
	if _, err := Load(); err == nil {
		t.Fatal("Load() unexpectedly succeeded")
	}
}

func TestOutcomeWebhookConfig(t *testing.T) {
	t.Setenv("CODEX_AGENT_TOKEN", "agent-token")
	t.Setenv("CODEX_AGENT_OUTCOME_WEBHOOK_URL", "http://127.0.0.1:19100/outcome")
	t.Setenv("CODEX_AGENT_OUTCOME_WEBHOOK_TIMEOUT_SECONDS", "3")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OutcomeWebhookURL != "http://127.0.0.1:19100/outcome" {
		t.Fatalf("OutcomeWebhookURL = %q", cfg.OutcomeWebhookURL)
	}
	if cfg.OutcomeWebhookTimeout != 3*time.Second {
		t.Fatalf("OutcomeWebhookTimeout = %s", cfg.OutcomeWebhookTimeout)
	}
}

func TestInvalidOutcomeWebhookURL(t *testing.T) {
	t.Setenv("CODEX_AGENT_TOKEN", "agent-token")
	t.Setenv("CODEX_AGENT_OUTCOME_WEBHOOK_URL", "ftp://127.0.0.1/outcome")
	if _, err := Load(); err == nil {
		t.Fatal("Load() unexpectedly succeeded")
	}
}
