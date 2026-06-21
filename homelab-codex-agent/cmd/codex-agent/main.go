package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"homelab-codex-agent/internal/cleanup"
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
	registry := cleanup.NewRegistry(
		cfg.Workdir,
		cfg.AttachmentRegistryPath,
		cfg.AttachmentRetention,
		cfg.CleanupInterval,
		logger,
	)
	if err := registry.Initialize(); err != nil {
		logger.Fatalf("attachment registry error: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	registry.Start(ctx)

	runner := codex.NewRunner(cfg.CodexBin, cfg.PromptPath, cfg.Timeout, cfg.MultimodalMode, logger)
	server := httpapi.NewServerWithRegistry(cfg, store, runner, registry, logger)
	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           server.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Printf("HTTP shutdown error: %v", err)
		}
	}()

	logger.Printf("listening on %s workdir=%s attachment_retention=%s cleanup_interval=%s", cfg.Listen, cfg.Workdir, cfg.AttachmentRetention, cfg.CleanupInterval)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatalf("server stopped: %v", err)
	}
}
