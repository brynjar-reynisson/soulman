package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"soulman/common/dephealth"
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

	// File log — must succeed; no file log = no durability guarantee
	fl, err := storage.NewFileLog(cfg.LogDir, storage.DefaultMaxFileSize)
	if err != nil {
		slog.Error("filelog init failed", "error", err)
		os.Exit(1)
	}
	defer fl.Close()

	registry := dephealth.NewRegistry()

	// Postgres — non-fatal; service starts and writes to file when DB is
	// down. dbHolder makes this recoverable: Reconnector retries in the
	// background, and every dependent (Writer, HTTP handlers, the
	// episodes consumer) reads through dbHolder so a later reconnect
	// takes effect without a restart.
	db, dbErr := storage.NewDB(ctx, cfg.DatabaseURL, cfg.Schema)
	if dbErr != nil {
		slog.Warn("postgres unavailable — writes go to file only until DB reconnects", "error", dbErr)
	}
	dbHolder := storage.NewDBHolder(db, registry)
	defer dbHolder.Close()
	// cancel() must run before dbHolder.Close() at shutdown so the
	// Reconnector's background goroutine stops ticking before the DB pool
	// it holds a reference to is closed — DBHolder.Close() does not nil
	// out its internal *DB, so a Reconnector tick landing after Close()
	// could Ping() or Close() an already-closed pool. Registering this
	// defer here (after the dbHolder.Close() defer above) makes it run
	// first under defer's LIFO order.
	defer cancel()

	reconnector := storage.NewReconnector(dbHolder, registry, cfg.DatabaseURL, cfg.Schema)
	go reconnector.Run(ctx)

	// Writer orchestrates file + DB writes
	w := storage.NewWriter(fl, dbHolder)

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
	// action-svc/NOTES.md). dbHolder may be disconnected; its WriteEpisode
	// returns ErrNotConnected in that case, and NATS NAKs and retries later.
	episodeCons, err := natsconsumer.NewMemoryWriteConsumer(cfg.NATSURL, cfg.EpisodesConsumerName, cfg.MemoryWriteSubject, dbHolder)
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
	srv := httpserver.New(dbHolder, registry, cfg.HTTPPort)
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
