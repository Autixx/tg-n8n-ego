package config

import (
	"errors"
	"fmt"
	"os"
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
)

type Config struct {
	Listen      string
	Token       string
	Workdir     string
	PromptPath  string
	Timeout     time.Duration
	CodexBin    string
	DefaultMode string
}

func Load() (Config, error) {
	timeoutSeconds, err := intEnv("CODEX_AGENT_TIMEOUT_SECONDS", defaultTimeoutSeconds)
	if err != nil {
		return Config{}, err
	}
	if timeoutSeconds <= 0 {
		return Config{}, errors.New("CODEX_AGENT_TIMEOUT_SECONDS must be positive")
	}

	cfg := Config{
		Listen:      stringEnv("CODEX_AGENT_LISTEN", defaultListen),
		Token:       strings.TrimSpace(os.Getenv("CODEX_AGENT_TOKEN")),
		Workdir:     stringEnv("CODEX_AGENT_WORKDIR", defaultWorkdir),
		PromptPath:  stringEnv("CODEX_AGENT_PROMPT", defaultPrompt),
		Timeout:     time.Duration(timeoutSeconds) * time.Second,
		CodexBin:    stringEnv("CODEX_AGENT_CODEX_BIN", defaultCodexBin),
		DefaultMode: stringEnv("CODEX_AGENT_MODE_DEFAULT", defaultMode),
	}

	if cfg.Token == "" {
		return Config{}, errors.New("CODEX_AGENT_TOKEN is required")
	}
	if !IsAllowedMode(cfg.DefaultMode) {
		return Config{}, fmt.Errorf("CODEX_AGENT_MODE_DEFAULT is not allowed: %s", cfg.DefaultMode)
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

func IsAllowedMode(mode string) bool {
	switch mode {
	case "abstract_idea", "structured_breakdown", "create_tasks":
		return true
	default:
		return false
	}
}
