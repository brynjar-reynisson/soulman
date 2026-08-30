package main

import (
	"crypto/rand"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"soulman/web-svc/auth"
	"soulman/web-svc/claudesession"
	"soulman/web-svc/config"
	"soulman/web-svc/filebrowser"
	"soulman/web-svc/httpserver"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	verifier := auth.NewVerifier(cfg.SupabaseURL, cfg.SupabaseJWTSecret, cfg.OwnerEmail)

	claudeRoots := make([]claudesession.Root, len(cfg.ClaudeProjectRoots))
	for i, r := range cfg.ClaudeProjectRoots {
		claudeRoots[i] = claudesession.Root{Label: r.Label, Path: r.Path}
	}

	fileBrowserRoots := make([]filebrowser.Root, len(cfg.FileBrowserRoots))
	for i, r := range cfg.FileBrowserRoots {
		fileBrowserRoots[i] = filebrowser.Root{Label: r.Label, Path: r.Path}
	}

	if cfg.BraveSearchAPIKey == "" {
		slog.Warn("BRAVE_SEARCH_API_KEY not set — web search requests will fail until it's configured")
	}

	shareLinkSecret := make([]byte, 32)
	if _, err := rand.Read(shareLinkSecret); err != nil {
		slog.Error("generating share link secret failed", "error", err)
		os.Exit(1)
	}

	srv := httpserver.New(cfg.HTTPPort, httpserver.Config{
		CORSAllowedOrigin:  cfg.CORSAllowedOrigin,
		PerceptionSvcURL:   cfg.PerceptionSvcURL,
		MemorySvcURL:       cfg.MemorySvcURL,
		ThinkingSvcURL:     cfg.ThinkingSvcURL,
		ActionSvcURL:       cfg.ActionSvcURL,
		ReportsRoot:        cfg.SoulmanRoot,
		ObsidianRoot:       cfg.ObsidianRoot,
		ClaudeProjectRoots: claudeRoots,
		FileBrowserRoots:   fileBrowserRoots,
		ShareLinkSecret:    shareLinkSecret,
		ShareLinkTTL:       cfg.ShareLinkTTL,
		BraveSearchAPIKey:  cfg.BraveSearchAPIKey,
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
