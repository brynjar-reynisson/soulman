package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"soulman/memory-svc/config"
	"soulman/memory-svc/httpserver"
	"soulman/memory-svc/natsconsumer"
	"soulman/memory-svc/storage"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// File log — must succeed; no file log = no durability guarantee
	fl, err := storage.NewFileLog(cfg.LogDir, storage.DefaultMaxFileSize)
	if err != nil {
		slog.Error("filelog init failed", "error", err)
		os.Exit(1)
	}
	defer fl.Close()

	// Postgres — non-fatal; service starts and writes to file when DB is down
	db, dbErr := storage.NewDB(ctx, cfg.DatabaseURL, cfg.Schema)
	if dbErr != nil {
		slog.Warn("postgres unavailable — writes go to file only until DB reconnects", "error", dbErr)
	}
	if db != nil {
		defer db.Close()
	}

	// Writer orchestrates file + DB writes
	w := storage.NewWriter(fl, db)

	// Replay any file entries that never made it to the DB
	if err := w.ReplayPending(ctx); err != nil {
		slog.Error("replay of pending file entries failed", "error", err)
	}

	// STIMULUS consumer
	cons, err := natsconsumer.New(cfg.NATSURL, cfg.ConsumerName, cfg.StimulusSubject, w)
	if err != nil {
		slog.Error("nats consumer init failed", "error", err)
		os.Exit(1)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		slog.Error("nats consumer start failed", "error", err)
		os.Exit(1)
	}

	// MEMORY_WRITE (episodes) consumer — wired independently of the STIMULUS
	// consumer above, so a hiccup in one never silently disables the other
	// (the "keep dual consumer setup independent" lesson documented in
	// action-svc/NOTES.md). db may be nil if Postgres is down; WriteEpisode
	// handles that safely (returns an error, NATS NAKs and retries later).
	episodeCons, err := natsconsumer.NewMemoryWriteConsumer(cfg.NATSURL, cfg.EpisodesConsumerName, cfg.MemoryWriteSubject, db)
	if err != nil {
		slog.Error("nats memory-write consumer init failed", "error", err)
		os.Exit(1)
	}
	defer episodeCons.Close()

	if err := episodeCons.Start(ctx); err != nil {
		slog.Error("nats memory-write consumer start failed", "error", err)
		os.Exit(1)
	}

	// HTTP server (non-blocking)
	srv := httpserver.New(db, cfg.HTTPPort)
	slog.Info("http listening", "port", cfg.HTTPPort)
	go func() {
		if err := srv.Start(); err != nil {
			slog.Error("http server failed", "error", err)
		}
	}()

	slog.Info("memory-svc started",
		"nats_url", cfg.NATSURL, "db_connected", dbErr == nil, "http_port", cfg.HTTPPort, "log_dir", cfg.LogDir)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("memory-svc shutting down")
}
