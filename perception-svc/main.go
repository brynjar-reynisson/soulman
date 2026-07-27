package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"soulman/perception-svc/config"
	"soulman/perception-svc/gmailwatcher"
	"soulman/perception-svc/httpserver"
	"soulman/perception-svc/logmonitor"
	"soulman/perception-svc/natspublish"
	"soulman/perception-svc/sysmonitor"
	"soulman/perception-svc/watcher"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cp := watcher.LoadCheckpoint(cfg.CheckpointPath)

	// NATS — non-fatal at startup for unreachable hosts; RetryOnFailedConnect
	// keeps trying in the background while the watcher and HTTP server start.
	pub, err := natspublish.New(cfg.NATSURL, cfg.StimulusSubject)
	if err != nil {
		slog.Error("natspublish init failed", "error", err)
		os.Exit(1)
	}
	defer pub.Close()

	w, err := watcher.New(cfg.WatchPaths, cp, pub, time.Duration(cfg.ReconcileInterval)*time.Second)
	if err != nil {
		slog.Error("watcher init failed", "error", err)
		os.Exit(1)
	}
	defer w.Close()

	w.Start(ctx)

	// Gmail channel is optional: if credentials aren't configured yet (the
	// one-time OAuth bootstrap hasn't been done), skip it entirely rather
	// than failing startup — folder-watcher stays fully functional either
	// way, per Perception module.md's adapter-isolation principle.
	if cfg.GmailClientID == "" || cfg.GmailClientSecret == "" || cfg.GmailRefreshToken == "" {
		slog.Warn("gmailwatcher: GMAIL_CLIENT_ID/SECRET/REFRESH_TOKEN not fully set, Gmail channel disabled")
	} else {
		gw, err := gmailwatcher.New(ctx, gmailwatcher.Config{
			ClientID:     cfg.GmailClientID,
			ClientSecret: cfg.GmailClientSecret,
			RefreshToken: cfg.GmailRefreshToken,
			Query:        cfg.GmailQuery,
			SeenLabel:    cfg.GmailSeenLabel,
			PollInterval: time.Duration(cfg.GmailPollIntervalSeconds) * time.Second,
		}, pub)
		if err != nil {
			slog.Warn("gmailwatcher: setup failed, Gmail channel disabled", "error", err)
		} else {
			defer gw.Close()
			gw.Start(ctx)
			slog.Info("gmailwatcher: started",
				"query", cfg.GmailQuery, "seen_label", cfg.GmailSeenLabel, "poll_interval_s", cfg.GmailPollIntervalSeconds)
		}
	}

	smChecks := make([]sysmonitor.CheckConfig, len(cfg.SystemMonitorChecks))
	for i, c := range cfg.SystemMonitorChecks {
		smChecks[i] = sysmonitor.CheckConfig{
			Type:                     c.Type,
			Path:                     c.Path,
			Name:                     c.Name,
			Target:                   c.Target,
			WarningThresholdPercent:  c.WarningThresholdPercent,
			CriticalThresholdPercent: c.CriticalThresholdPercent,
		}
	}
	sm := sysmonitor.New(smChecks, pub, time.Duration(cfg.SystemMonitorPollIntervalSeconds)*time.Second)
	defer sm.Close()
	sm.Start(ctx)
	slog.Info("sysmonitor: started", "checks", len(smChecks), "poll_interval_s", cfg.SystemMonitorPollIntervalSeconds)

	lm, err := logmonitor.New(cfg.LogDir, pub, cfg.LogMonitorCheckpointPath, time.Duration(cfg.LogMonitorReconcileIntervalSeconds)*time.Second)
	if err != nil {
		slog.Error("logmonitor init failed", "error", err)
		os.Exit(1)
	}
	defer lm.Close()
	lm.Start(ctx)
	slog.Info("logmonitor: started", "log_dir", cfg.LogDir, "reconcile_interval_s", cfg.LogMonitorReconcileIntervalSeconds)

	srv := httpserver.New(cfg.HTTPPort, cfg.WatchPaths, pub.Status, pub, sm.Status)
	go func() {
		slog.Info("http listening", "port", cfg.HTTPPort)
		if err := srv.Start(); err != nil {
			slog.Error("http server failed", "error", err)
		}
	}()

	slog.Info("perception-svc started",
		"nats_url", cfg.NATSURL, "http_port", cfg.HTTPPort, "watching", cfg.WatchPaths, "checkpoint", cfg.CheckpointPath)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("perception-svc shutting down")
}
