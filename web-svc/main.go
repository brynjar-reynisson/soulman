package main

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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

	videoShareSecret, err := loadOrCreateSecret(filepath.Join(cfg.SoulmanRoot, "video-share-secret"))
	if err != nil {
		slog.Error("loading video share secret failed", "error", err)
		os.Exit(1)
	}

	srv := httpserver.New(cfg.HTTPPort, httpserver.Config{
		CORSAllowedOrigin:  cfg.CORSAllowedOrigin,
		PerceptionSvcURL:   cfg.PerceptionSvcURL,
		MemorySvcURL:       cfg.MemorySvcURL,
		ThinkingSvcURL:     cfg.ThinkingSvcURL,
		ActionSvcURL:       cfg.ActionSvcURL,
		ProjectsSvcURL:     cfg.ProjectsSvcURL,
		ReportsRoot:        cfg.SoulmanRoot,
		ObsidianRoot:       cfg.ObsidianRoot,
		DiaryRoot:          cfg.DiaryRoot,
		ClaudeProjectRoots: claudeRoots,
		FileBrowserRoots:   fileBrowserRoots,
		ShareLinkSecret:    shareLinkSecret,
		ShareLinkTTL:       cfg.ShareLinkTTL,
		VideoRoot:          cfg.VideoRoot,
		VideoShareSecret:   videoShareSecret,
		VideoShareTTL:      cfg.VideoShareTTL,
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

// loadOrCreateSecret returns the hex-encoded secret stored at path,
// generating and saving 32 random bytes on first run. Video links live for
// days, so unlike the file-share secret this one must survive restarts
// (web-svc is rebuilt and restarted on every login).
func loadOrCreateSecret(path string) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		if secret, err := hex.DecodeString(strings.TrimSpace(string(data))); err == nil && len(secret) >= 32 {
			return secret, nil
		}
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(secret)), 0o600); err != nil {
		return nil, err
	}
	return secret, nil
}
