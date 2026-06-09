package main

import (
	"log"
	"net/http"
	"os"

	"homelab-codex-agent/internal/codex"
	"homelab-codex-agent/internal/config"
	"homelab-codex-agent/internal/httpapi"
	"homelab-codex-agent/internal/jobs"
)

func main() {
	logger := log.New(os.Stdout, "codex-agent ", log.LstdFlags|log.LUTC|log.Lmsgprefix)

	cfg, err := config.Load()
	if err != nil {
		logger.Fatalf("config error: %v", err)
	}

	store := jobs.NewStore(cfg.Workdir, logger)
	runner := codex.NewRunner(cfg.CodexBin, cfg.PromptPath, cfg.Timeout, logger)
	server := httpapi.NewServer(cfg, store, runner, logger)

	logger.Printf("listening on %s workdir=%s", cfg.Listen, cfg.Workdir)
	if err := http.ListenAndServe(cfg.Listen, server.Routes()); err != nil {
		logger.Fatalf("server stopped: %v", err)
	}
}
