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
}

func TestInvalidMultimodalMode(t *testing.T) {
	t.Setenv("CODEX_AGENT_TOKEN", "agent-token")
	t.Setenv("CODEX_AGENT_MULTIMODAL_MODE", "maybe")
	if _, err := Load(); err == nil {
		t.Fatal("Load() unexpectedly succeeded")
	}
}
