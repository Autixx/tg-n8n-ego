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
	defaultPromptV2       = "/opt/codex-agent/prompts/projectego-decompose-v2.md"
	defaultTimeoutSeconds = 240
	defaultCodexBin       = "codex"
	defaultMode           = "structured_breakdown"
	defaultMaxAttachments = 4
	defaultMaxAttachBytes = 10 * 1024 * 1024
	defaultAllowImages    = true
	defaultMultimodalMode = "auto"
	defaultRetentionHours = 24
	defaultCleanupMinutes = 60
	defaultSessionEnabled = true
	defaultSessionTurns   = 10
	defaultSessionAge     = 360
	defaultSessionPurpose = "projectego-decompose"
	defaultRunner         = "exec"
	defaultRunnerFallback = "exec"
	defaultAppServerURL   = "unix:///opt/codex-agent/codex-app-server.sock"
	defaultWebhookTimeout = 10
)

type Config struct {
	Listen       string
	Token        string
	Workdir      string
	PromptPath   string
	PromptPathV2 string
	Timeout      time.Duration
	CodexBin     string
	DefaultMode  string

	DashboardAttachmentToken string
	MaxAttachments           int
	MaxAttachmentBytes       int64
	AllowImageAttachments    bool
	MultimodalMode           string
	AttachmentRegistryPath   string
	AttachmentRetention      time.Duration
	CleanupInterval          time.Duration

	SessionEnabled   bool
	SessionMaxTurns  int
	SessionMaxAge    time.Duration
	SessionPurpose   string
	SessionStorePath string

	RunnerBackend         string
	RunnerFallback        string
	AppServerURL          string
	AppServerSocketPath   string
	AppServerTurnTimeout  time.Duration
	OutcomeWebhookURL     string
	OutcomeWebhookTimeout time.Duration
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
	sessionEnabled, err := boolEnv("CODEX_AGENT_SESSION_ENABLED", defaultSessionEnabled)
	if err != nil {
		return Config{}, err
	}
	sessionTurns, err := intEnv("CODEX_AGENT_SESSION_MAX_TURNS", defaultSessionTurns)
	if err != nil {
		return Config{}, err
	}
	if sessionTurns <= 0 {
		return Config{}, errors.New("CODEX_AGENT_SESSION_MAX_TURNS must be positive")
	}
	sessionAgeMinutes, err := intEnv("CODEX_AGENT_SESSION_MAX_AGE_MINUTES", defaultSessionAge)
	if err != nil {
		return Config{}, err
	}
	if sessionAgeMinutes <= 0 {
		return Config{}, errors.New("CODEX_AGENT_SESSION_MAX_AGE_MINUTES must be positive")
	}
	webhookTimeoutSeconds, err := intEnv("CODEX_AGENT_OUTCOME_WEBHOOK_TIMEOUT_SECONDS", defaultWebhookTimeout)
	if err != nil {
		return Config{}, err
	}
	if webhookTimeoutSeconds <= 0 {
		return Config{}, errors.New("CODEX_AGENT_OUTCOME_WEBHOOK_TIMEOUT_SECONDS must be positive")
	}
	workdir := stringEnv("CODEX_AGENT_WORKDIR", defaultWorkdir)

	cfg := Config{
		Listen:       stringEnv("CODEX_AGENT_LISTEN", defaultListen),
		Token:        strings.TrimSpace(os.Getenv("CODEX_AGENT_TOKEN")),
		Workdir:      workdir,
		PromptPath:   stringEnv("CODEX_AGENT_PROMPT", defaultPrompt),
		PromptPathV2: stringEnv("CODEX_AGENT_PROMPT_V2", defaultPromptV2),
		Timeout:      time.Duration(timeoutSeconds) * time.Second,
		CodexBin:     stringEnv("CODEX_AGENT_CODEX_BIN", defaultCodexBin),
		DefaultMode:  stringEnv("CODEX_AGENT_MODE_DEFAULT", defaultMode),

		DashboardAttachmentToken: strings.TrimSpace(os.Getenv("CODEX_AGENT_DASHBOARD_ATTACHMENT_TOKEN")),
		MaxAttachments:           maxAttachments,
		MaxAttachmentBytes:       maxAttachmentBytes,
		AllowImageAttachments:    allowImages,
		MultimodalMode:           strings.ToLower(stringEnv("CODEX_AGENT_MULTIMODAL_MODE", defaultMultimodalMode)),
		AttachmentRegistryPath:   stringEnv("CODEX_AGENT_ATTACHMENT_REGISTRY", filepath.Join(workdir, "attachment-registry.xml")),
		AttachmentRetention:      time.Duration(retentionHours) * time.Hour,
		CleanupInterval:          time.Duration(cleanupMinutes) * time.Minute,
		SessionEnabled:           sessionEnabled,
		SessionMaxTurns:          sessionTurns,
		SessionMaxAge:            time.Duration(sessionAgeMinutes) * time.Minute,
		SessionPurpose:           stringEnv("CODEX_AGENT_SESSION_PURPOSE", defaultSessionPurpose),
		SessionStorePath:         stringEnv("CODEX_AGENT_SESSION_STORE_PATH", filepath.Join(workdir, "codex-sessions.json")),
		RunnerBackend:            strings.ToLower(stringEnv("CODEX_AGENT_RUNNER", defaultRunner)),
		RunnerFallback:           strings.ToLower(stringEnv("CODEX_AGENT_RUNNER_FALLBACK", defaultRunnerFallback)),
		AppServerURL:             stringEnv("CODEX_AGENT_APP_SERVER_URL", defaultAppServerURL),
		AppServerTurnTimeout:     time.Duration(timeoutSeconds) * time.Second,
		OutcomeWebhookURL:        strings.TrimSpace(os.Getenv("CODEX_AGENT_OUTCOME_WEBHOOK_URL")),
		OutcomeWebhookTimeout:    time.Duration(webhookTimeoutSeconds) * time.Second,
	}
	cfg.AppServerSocketPath, err = parseAppServerSocketPath(cfg.AppServerURL)
	if err != nil {
		return Config{}, err
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
	if !IsAllowedRunner(cfg.RunnerBackend) {
		return Config{}, fmt.Errorf("CODEX_AGENT_RUNNER is not allowed: %s", cfg.RunnerBackend)
	}
	if !IsAllowedRunnerFallback(cfg.RunnerFallback) {
		return Config{}, fmt.Errorf("CODEX_AGENT_RUNNER_FALLBACK is not allowed: %s", cfg.RunnerFallback)
	}
	if err := validateOutcomeWebhookURL(cfg.OutcomeWebhookURL); err != nil {
		return Config{}, err
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

func IsAllowedRunner(runner string) bool {
	switch runner {
	case "exec", "appserver":
		return true
	default:
		return false
	}
}

func IsAllowedRunnerFallback(runner string) bool {
	switch runner {
	case "exec", "off":
		return true
	default:
		return false
	}
}

func parseAppServerSocketPath(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || rawURL == "off" {
		return "", nil
	}
	if !strings.HasPrefix(rawURL, "unix://") {
		return "", fmt.Errorf("CODEX_AGENT_APP_SERVER_URL must be unix://PATH or off")
	}
	path := strings.TrimPrefix(rawURL, "unix://")
	if path == "" {
		return "", errors.New("CODEX_AGENT_APP_SERVER_URL unix socket path is required")
	}
	return path, nil
}

func validateOutcomeWebhookURL(rawURL string) error {
	if rawURL == "" {
		return nil
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return errors.New("CODEX_AGENT_OUTCOME_WEBHOOK_URL must start with http:// or https://")
	}
	return nil
}
