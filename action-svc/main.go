package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"soulman/action-svc/audit"
	"soulman/action-svc/calendar"
	"soulman/action-svc/config"
	"soulman/action-svc/dispatch"
	"soulman/action-svc/dnd"
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
		slog.Error("config load failed", "error", err)
		os.Exit(1)
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
		slog.Error("unsupported REPORT_NOTIFIER", "report_notifier", cfg.ReportNotifier)
		os.Exit(1)
	}
	notifier = feign.WrapNotifier(gate, notifier)

	// Audit log — one JSONL line per Discord send attempt (what, when, and
	// why — see action-svc/audit and action-svc/NOTES.md), tagged per
	// origin below. Shared across every reason so concurrent sends from
	// different subsystems never interleave partial writes.
	auditLog := audit.New(filepath.Join(cfg.SoulmanRoot, "logs", "discord-audit.jsonl"))
	dailyNotifier := audit.Wrap(auditLog, notifier, "daily-digest")
	batchRealNotifier := audit.Wrap(auditLog, notifier, "important-batch")

	// Do-not-disturb window — see
	// docs/superpowers/specs/2026-07-27-discord-do-not-disturb-design.md.
	// Only the Batcher's real-time notification path is gated; the daily
	// digest cron (sched, below) keeps using its own audit-wrapped
	// notifier, unaffected by DND. If DNDEnabled is false, batcherNotifier
	// stays the plain (audit-wrapped, feign-wrapped) batchRealNotifier —
	// behavior identical to pre-DND.
	dndWindow := dnd.Window{Start: cfg.DNDStart, End: cfg.DNDEnd}
	pendingPath := filepath.Join(cfg.SoulmanRoot, "logs", "dnd-pending.txt")
	batcherNotifier := batchRealNotifier
	if cfg.DNDEnabled {
		batcherNotifier = dnd.WrapNotifier(dndWindow, pendingPath, batchRealNotifier)
		dndFlusher := dnd.NewFlusher(dndWindow, pendingPath, batchRealNotifier) // starts its own background loop once Start is called
		dndFlusher.Start()
		defer dndFlusher.Stop()
	}

	// Batches important-email Discord notifications from the
	// triage_gmail_email dispatch handler (30s grace / 2min max-wait — see
	// docs/superpowers/specs/2026-07-18-gmail-triage-action-design.md).
	// Reuses the same (feign-wrapped, and — if DND is enabled — DND-wrapped)
	// notifier the daily cron already sends through.
	batcher := notifybatch.New(notifybatch.DefaultGrace, notifybatch.DefaultMaxWait, batcherNotifier)

	// NATS is non-fatal at startup: the dispatch side degrades until
	// reconnect, but the HTTP server and the daily cron don't depend on it.
	var publisher *natsclient.Publisher
	nc, natsErr := natsclient.Connect(cfg.NATSURL)
	if natsErr != nil {
		slog.Warn("nats unavailable — dispatch degraded until reconnect", "error", natsErr)
	} else {
		defer nc.Close()

		var pubErr error
		publisher, pubErr = natsclient.NewPublisher(ctx, nc, cfg.MemoryWriteSubject)
		if pubErr != nil {
			slog.Warn("nats publisher setup failed — outcome records degraded", "error", pubErr)
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
			slog.Warn("nats consumer setup failed", "error", consErr)
		} else if startErr := consumer.Start(ctx); startErr != nil {
			slog.Warn("nats consumer start failed", "error", startErr)
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
	sched := scheduler.New(cfg.SoulmanRoot, cfg.ReportSendTime, dailyNotifier, schedPublisher, gate)
	sched.Start()
	defer sched.Stop()

	// School event reminders — prod-only via cfg.SchoolEnabled (see
	// docs/superpowers/specs/2026-09-03-school-email-events-design.md).
	// Deliberately its own plain Discord notifier, NOT feign- or DND-wrapped —
	// this feature ships live from day one.
	if cfg.SchoolEnabled {
		var schoolDiscord notify.Notifier = notify.NewDiscordNotifier(cfg.DiscordBotToken, cfg.DiscordChannelID)
		schoolDiscord = audit.Wrap(auditLog, schoolDiscord, "school-reminder")

		var inviter scheduler.EventInviter
		if cfg.CalendarClientID != "" && cfg.CalendarClientSecret != "" && cfg.CalendarRefreshToken != "" {
			calClient, calErr := calendar.New(ctx, cfg.CalendarClientID, cfg.CalendarClientSecret, cfg.CalendarRefreshToken)
			if calErr != nil {
				slog.Warn("calendar client init failed — calendar invites will fail until fixed", "error", calErr)
			} else {
				inviter = calClient
			}
		} else {
			slog.Warn("CALENDAR_CLIENT_ID/SECRET/REFRESH_TOKEN not fully set — calendar invites will fail until configured")
		}

		schoolSched := scheduler.NewSchoolEventScheduler(cfg.SoulmanRoot, cfg.SchoolNotifyTime, cfg.SchoolCalendarRecipientEmails, schoolDiscord, inviter)
		schoolSched.Start()
		defer schoolSched.Stop()
	}

	// HTTP server (non-blocking)
	srv := httpserver.New(cfg.HTTPPort)
	go func() {
		slog.Info("http listening", "port", cfg.HTTPPort)
		if err := srv.Start(); err != nil {
			slog.Error("http server failed", "error", err)
		}
	}()

	slog.Info("action-svc started",
		"nats_url", cfg.NATSURL, "nats_connected", natsErr == nil, "http_port", cfg.HTTPPort,
		"root", cfg.SoulmanRoot, "notifier", cfg.ReportNotifier, "feign_mode", cfg.FeignMode,
		"dnd_enabled", cfg.DNDEnabled, "dnd_start", cfg.DNDStart, "dnd_end", cfg.DNDEnd)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("action-svc shutting down")
}
