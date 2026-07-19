package main

import (
	"log"
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
		log.Fatalf("config: %v", err)
	}

	verifier := auth.NewVerifier(cfg.SupabaseURL, cfg.SupabaseJWTSecret, cfg.OwnerEmail)

	srv := httpserver.New(cfg.HTTPPort, httpserver.Config{
		CORSAllowedOrigin: cfg.CORSAllowedOrigin,
		PerceptionSvcURL:  cfg.PerceptionSvcURL,
		MemorySvcURL:      cfg.MemorySvcURL,
		ThinkingSvcURL:    cfg.ThinkingSvcURL,
		ActionSvcURL:      cfg.ActionSvcURL,
		ReportsRoot:       cfg.SoulmanRoot,
	}, verifier)

	go func() {
		log.Printf("HTTP listening on :%s", cfg.HTTPPort)
		if err := srv.Start(); err != nil {
			log.Printf("http: %v", err)
		}
	}()

	log.Printf("web-svc started (HTTP=:%s, owner=%s)", cfg.HTTPPort, cfg.OwnerEmail)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Printf("web-svc shutting down")
}
