package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"soulman/perception-svc/config"
	"soulman/perception-svc/gmailwatcher"
	"soulman/perception-svc/httpserver"
	"soulman/perception-svc/natspublish"
	"soulman/perception-svc/sysmonitor"
	"soulman/perception-svc/watcher"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cp := watcher.LoadCheckpoint(cfg.CheckpointPath)

	// NATS — non-fatal at startup for unreachable hosts; RetryOnFailedConnect
	// keeps trying in the background while the watcher and HTTP server start.
	pub, err := natspublish.New(cfg.NATSURL, cfg.StimulusSubject)
	if err != nil {
		log.Fatalf("natspublish: %v", err)
	}
	defer pub.Close()

	w, err := watcher.New(cfg.WatchPaths, cp, pub, time.Duration(cfg.ReconcileInterval)*time.Second)
	if err != nil {
		log.Fatalf("watcher: %v", err)
	}
	defer w.Close()

	w.Start(ctx)

	// Gmail channel is optional: if credentials aren't configured yet (the
	// one-time OAuth bootstrap hasn't been done), skip it entirely rather
	// than failing startup — folder-watcher stays fully functional either
	// way, per Perception module.md's adapter-isolation principle.
	if cfg.GmailClientID == "" || cfg.GmailClientSecret == "" || cfg.GmailRefreshToken == "" {
		log.Printf("gmailwatcher: GMAIL_CLIENT_ID/SECRET/REFRESH_TOKEN not fully set, Gmail channel disabled")
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
			log.Printf("gmailwatcher: setup failed, Gmail channel disabled: %v", err)
		} else {
			defer gw.Close()
			gw.Start(ctx)
			log.Printf("gmailwatcher: started (query=%q, seen_label=%q, poll_interval=%ds)",
				cfg.GmailQuery, cfg.GmailSeenLabel, cfg.GmailPollIntervalSeconds)
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
	log.Printf("sysmonitor: started (checks=%d, poll_interval=%ds)", len(smChecks), cfg.SystemMonitorPollIntervalSeconds)

	srv := httpserver.New(cfg.HTTPPort, cfg.WatchPaths, pub.Status, pub)
	go func() {
		log.Printf("HTTP listening on :%s", cfg.HTTPPort)
		if err := srv.Start(); err != nil {
			log.Printf("http: %v", err)
		}
	}()

	log.Printf("perception-svc started (NATS=%s, HTTP=:%s, watching=%v, checkpoint=%s)",
		cfg.NATSURL, cfg.HTTPPort, cfg.WatchPaths, cfg.CheckpointPath)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Printf("perception-svc shutting down")
}
