package main

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"soulman/web-svc/auth"
	"soulman/web-svc/config"
	"soulman/web-svc/httpserver"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	verifier := auth.NewVerifier(cfg.SupabaseURL, cfg.SupabaseJWTSecret, cfg.OwnerEmail)

	srv := httpserver.New(cfg.HTTPPort, httpserver.Config{
		CORSAllowedOrigin: cfg.CORSAllowedOrigin,
		PerceptionSvcURL:  cfg.PerceptionSvcURL,
		MemorySvcURL:      cfg.MemorySvcURL,
		ThinkingSvcURL:    cfg.ThinkingSvcURL,
		ActionSvcURL:      cfg.ActionSvcURL,
		ReportsRoot:       cfg.SoulmanRoot,
		ObsidianRoot:      cfg.ObsidianRoot,
	}, verifier)

	go func() {
		slog.Info("http listening", "port", cfg.HTTPPort)
		if err := srv.Start(); err != nil {
			slog.Error("http server failed", "error", err)
		}
	}()

	slog.Info("web-svc started", "http_port", cfg.HTTPPort, "owner", cfg.OwnerEmail)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("web-svc shutting down")
}
