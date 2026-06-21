package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultListen         = "127.0.0.1:19090"
	defaultWorkdir        = "/opt/codex-agent"
	defaultPrompt         = "/opt/codex-agent/prompts/projectego_router.md"
	defaultTimeoutSeconds = 240
	defaultCodexBin       = "codex"
	defaultMode           = "structured_breakdown"
	defaultMaxAttachments = 4
	defaultMaxAttachBytes = 10 * 1024 * 1024
	defaultAllowImages    = true
	defaultMultimodalMode = "auto"
	defaultRetentionHours = 24
	defaultCleanupMinutes = 60
)

type Config struct {
	Listen      string
	Token       string
	Workdir     string
	PromptPath  string
	Timeout     time.Duration
	CodexBin    string
	DefaultMode string

	DashboardAttachmentToken string
	MaxAttachments           int
	MaxAttachmentBytes       int64
	AllowImageAttachments    bool
	MultimodalMode           string
	AttachmentRegistryPath   string
	AttachmentRetention      time.Duration
	CleanupInterval          time.Duration
}

func Load() (Config, error) {
	timeoutSeconds, err := intEnv("CODEX_AGENT_TIMEOUT_SECONDS", defaultTimeoutSeconds)
	if err != nil {
		return Config{}, err
	}
	if timeoutSeconds <= 0 {
		return Config{}, errors.New("CODEX_AGENT_TIMEOUT_SECONDS must be positive")
	}
	maxAttachments, err := intEnv("CODEX_AGENT_MAX_ATTACHMENTS", defaultMaxAttachments)
	if err != nil {
		return Config{}, err
	}
	if maxAttachments <= 0 {
		return Config{}, errors.New("CODEX_AGENT_MAX_ATTACHMENTS must be positive")
	}
	maxAttachmentBytes, err := int64Env("CODEX_AGENT_MAX_ATTACHMENT_BYTES", defaultMaxAttachBytes)
	if err != nil {
		return Config{}, err
	}
	if maxAttachmentBytes <= 0 {
		return Config{}, errors.New("CODEX_AGENT_MAX_ATTACHMENT_BYTES must be positive")
	}
	allowImages, err := boolEnv("CODEX_AGENT_ALLOW_IMAGE_ATTACHMENTS", defaultAllowImages)
	if err != nil {
		return Config{}, err
	}
	retentionHours, err := intEnv("CODEX_AGENT_ATTACHMENT_RETENTION_HOURS", defaultRetentionHours)
	if err != nil {
		return Config{}, err
	}
	if retentionHours <= 0 {
		return Config{}, errors.New("CODEX_AGENT_ATTACHMENT_RETENTION_HOURS must be positive")
	}
	cleanupMinutes, err := intEnv("CODEX_AGENT_CLEANUP_INTERVAL_MINUTES", defaultCleanupMinutes)
	if err != nil {
		return Config{}, err
	}
	if cleanupMinutes <= 0 {
		return Config{}, errors.New("CODEX_AGENT_CLEANUP_INTERVAL_MINUTES must be positive")
	}
	workdir := stringEnv("CODEX_AGENT_WORKDIR", defaultWorkdir)

	cfg := Config{
		Listen:      stringEnv("CODEX_AGENT_LISTEN", defaultListen),
		Token:       strings.TrimSpace(os.Getenv("CODEX_AGENT_TOKEN")),
		Workdir:     workdir,
		PromptPath:  stringEnv("CODEX_AGENT_PROMPT", defaultPrompt),
		Timeout:     time.Duration(timeoutSeconds) * time.Second,
		CodexBin:    stringEnv("CODEX_AGENT_CODEX_BIN", defaultCodexBin),
		DefaultMode: stringEnv("CODEX_AGENT_MODE_DEFAULT", defaultMode),

		DashboardAttachmentToken: strings.TrimSpace(os.Getenv("CODEX_AGENT_DASHBOARD_ATTACHMENT_TOKEN")),
		MaxAttachments:           maxAttachments,
		MaxAttachmentBytes:       maxAttachmentBytes,
		AllowImageAttachments:    allowImages,
		MultimodalMode:           strings.ToLower(stringEnv("CODEX_AGENT_MULTIMODAL_MODE", defaultMultimodalMode)),
		AttachmentRegistryPath:   stringEnv("CODEX_AGENT_ATTACHMENT_REGISTRY", filepath.Join(workdir, "attachment-registry.xml")),
		AttachmentRetention:      time.Duration(retentionHours) * time.Hour,
		CleanupInterval:          time.Duration(cleanupMinutes) * time.Minute,
	}

	if cfg.Token == "" {
		return Config{}, errors.New("CODEX_AGENT_TOKEN is required")
	}
	if !IsAllowedMode(cfg.DefaultMode) {
		return Config{}, fmt.Errorf("CODEX_AGENT_MODE_DEFAULT is not allowed: %s", cfg.DefaultMode)
	}
	if !IsAllowedMultimodalMode(cfg.MultimodalMode) {
		return Config{}, fmt.Errorf("CODEX_AGENT_MULTIMODAL_MODE is not allowed: %s", cfg.MultimodalMode)
	}
	return cfg, nil
}

func stringEnv(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func intEnv(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return parsed, nil
}

func int64Env(name string, fallback int64) (int64, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return parsed, nil
}

func boolEnv(name string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", name, err)
	}
	return parsed, nil
}

func IsAllowedMode(mode string) bool {
	switch mode {
	case "abstract_idea", "structured_breakdown", "create_tasks":
		return true
	default:
		return false
	}
}

func IsAllowedMultimodalMode(mode string) bool {
	switch mode {
	case "auto", "enabled", "disabled":
		return true
	default:
		return false
	}
}
