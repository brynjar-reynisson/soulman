package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"soulman/action-svc/config"
	"soulman/action-svc/dispatch"
	"soulman/action-svc/feign"
	"soulman/action-svc/httpserver"
	"soulman/action-svc/natsclient"
	"soulman/action-svc/notifybatch"
	"soulman/action-svc/notify"
	"soulman/action-svc/scheduler"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Feign gate — see docs/superpowers/specs/2026-07-19-action-svc-feign-mode-design.md.
	// When enabled, outbound side effects (starting with Discord sends) are
	// recorded to logs/feigned-actions.jsonl instead of actually happening.
	gate := feign.New(cfg.FeignMode, filepath.Join(cfg.SoulmanRoot, "logs", "feigned-actions.jsonl"))

	// Notifier — Discord is the only implementation in v1. Built regardless
	// of whether DISCORD_BOT_TOKEN/DISCORD_CHANNEL_ID are set; a missing
	// token surfaces as a Send failure, handled like any other notifier
	// failure (retried, then logged) rather than a startup crash. Always
	// wrapped with the feign gate — a transparent passthrough when disabled.
	var notifier notify.Notifier
	switch cfg.ReportNotifier {
	case "discord":
		notifier = notify.NewDiscordNotifier(cfg.DiscordBotToken, cfg.DiscordChannelID)
	default:
		log.Fatalf("unsupported REPORT_NOTIFIER %q", cfg.ReportNotifier)
	}
	notifier = feign.WrapNotifier(gate, notifier)

	// Batches important-email Discord notifications from the
	// triage_gmail_email dispatch handler (30s grace / 2min max-wait — see
	// docs/superpowers/specs/2026-07-18-gmail-triage-action-design.md).
	// Reuses the same (feign-wrapped) notifier the daily cron already sends
	// through.
	batcher := notifybatch.New(notifybatch.DefaultGrace, notifybatch.DefaultMaxWait, notifier)

	// NATS is non-fatal at startup: the dispatch side degrades until
	// reconnect, but the HTTP server and the daily cron don't depend on it.
	var publisher *natsclient.Publisher
	nc, natsErr := natsclient.Connect(cfg.NATSURL)
	if natsErr != nil {
		log.Printf("WARNING: nats unavailable (%v) — dispatch degraded until reconnect", natsErr)
	} else {
		defer nc.Close()

		var pubErr error
		publisher, pubErr = natsclient.NewPublisher(ctx, nc, cfg.MemoryWriteSubject)
		if pubErr != nil {
			log.Printf("WARNING: nats publisher setup failed (%v) — outcome records degraded", pubErr)
		}

		// dispatchPublisher stays a true nil interface (not a typed-nil
		// *natsclient.Publisher) when publisher setup failed above, so
		// Dispatcher's `d.publisher == nil` check (dispatch.go) behaves
		// correctly instead of comparing a non-nil interface wrapping a nil
		// pointer. The durable thinking.request consumer below must come up
		// independently of whether the MEMORY_WRITE publisher succeeded —
		// it's the actual fix for the incident this plan exists to close,
		// and must never be gated on an unrelated stream's provisioning.
		var dispatchPublisher dispatch.Publisher
		if publisher != nil {
			dispatchPublisher = publisher
		}
		disp := dispatch.New(cfg.SoulmanRoot, dispatchPublisher, batcher, gate)
		consumer, consErr := natsclient.NewConsumer(nc, cfg.ActionSvcConsumerName, cfg.ThinkingRequestSubject, disp.Handle)
		if consErr != nil {
			log.Printf("WARNING: nats consumer setup failed: %v", consErr)
		} else if startErr := consumer.Start(ctx); startErr != nil {
			log.Printf("WARNING: nats consumer start failed: %v", startErr)
		} else {
			defer consumer.Close()
		}
	}

	// Scheduler runs independently of NATS — a stalled cron doesn't block
	// new error entries, and a NATS outage doesn't prevent yesterday's
	// report from being sent.
	var schedPublisher scheduler.OutcomePublisher
	if publisher != nil {
		schedPublisher = publisher
	}
	sched := scheduler.New(cfg.SoulmanRoot, cfg.ReportSendTime, notifier, schedPublisher, gate)
	sched.Start()
	defer sched.Stop()

	// HTTP server (non-blocking)
	srv := httpserver.New(cfg.HTTPPort)
	go func() {
		log.Printf("HTTP listening on :%s", cfg.HTTPPort)
		if err := srv.Start(); err != nil {
			log.Printf("http: %v", err)
		}
	}()

	log.Printf("action-svc started (NATS=%s connected=%v, HTTP=:%s, root=%s, notifier=%s, feign_mode=%v)",
		cfg.NATSURL, natsErr == nil, cfg.HTTPPort, cfg.SoulmanRoot, cfg.ReportNotifier, cfg.FeignMode)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Printf("action-svc shutting down")
}
