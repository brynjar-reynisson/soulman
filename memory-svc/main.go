package main

import (
	"context"
	"log"
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
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// File log — must succeed; no file log = no durability guarantee
	fl, err := storage.NewFileLog(cfg.LogDir, storage.DefaultMaxFileSize)
	if err != nil {
		log.Fatalf("filelog: %v", err)
	}
	defer fl.Close()

	// Postgres — non-fatal; service starts and writes to file when DB is down
	db, dbErr := storage.NewDB(ctx, cfg.DatabaseURL, cfg.Schema)
	if dbErr != nil {
		log.Printf("WARNING: postgres unavailable (%v) — writes go to file only until DB reconnects", dbErr)
	}
	if db != nil {
		defer db.Close()
	}

	// Writer orchestrates file + DB writes
	w := storage.NewWriter(fl, db)

	// Replay any file entries that never made it to the DB
	if err := w.ReplayPending(ctx); err != nil {
		log.Printf("replay: %v", err)
	}

	// STIMULUS consumer
	cons, err := natsconsumer.New(cfg.NATSURL, cfg.ConsumerName, cfg.StimulusSubject, w)
	if err != nil {
		log.Fatalf("nats: %v", err)
	}
	defer cons.Close()

	if err := cons.Start(ctx); err != nil {
		log.Fatalf("nats start: %v", err)
	}

	// MEMORY_WRITE (episodes) consumer — wired independently of the STIMULUS
	// consumer above, so a hiccup in one never silently disables the other
	// (the "keep dual consumer setup independent" lesson documented in
	// action-svc/NOTES.md). db may be nil if Postgres is down; WriteEpisode
	// handles that safely (returns an error, NATS NAKs and retries later).
	episodeCons, err := natsconsumer.NewMemoryWriteConsumer(cfg.NATSURL, cfg.EpisodesConsumerName, cfg.MemoryWriteSubject, db)
	if err != nil {
		log.Fatalf("nats (memory write): %v", err)
	}
	defer episodeCons.Close()

	if err := episodeCons.Start(ctx); err != nil {
		log.Fatalf("nats start (memory write): %v", err)
	}

	// HTTP server (non-blocking)
	srv := httpserver.New(db, cfg.HTTPPort)
	log.Printf("HTTP listening on :%s", cfg.HTTPPort)
	go func() {
		if err := srv.Start(); err != nil {
			log.Printf("http: %v", err)
		}
	}()

	log.Printf("memory-svc started (NATS=%s, DB=%v, HTTP=:%s, log=%s)",
		cfg.NATSURL, dbErr == nil, cfg.HTTPPort, cfg.LogDir)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Printf("memory-svc shutting down")
}
