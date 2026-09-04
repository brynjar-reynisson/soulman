package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"soulman/projects-svc/config"
	"soulman/projects-svc/dispatch"
	"soulman/projects-svc/httpserver"
	"soulman/projects-svc/launcher"
	"soulman/projects-svc/store"
)

func main() {
	cfg := config.Load()

	ctx := context.Background()
	st, err := store.New(ctx, cfg.DatabaseURL, cfg.Schema)
	if err != nil {
		slog.Error("store init failed", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	launchFunc := func(project store.Project, prompt store.Prompt) error {
		return launcher.Launch(
			launcher.Project{Name: project.Name, Path: project.Path},
			launcher.Prompt{ID: prompt.ID, TaskName: prompt.TaskName, PromptText: prompt.PromptText},
			cfg.NotifyPort,
		)
	}
	dispatcher := dispatch.New(st, launchFunc)

	mainSrv := httpserver.New(st, dispatcher)
	notifySrv := httpserver.NewNotifyServer(st, dispatcher)

	go func() {
		slog.Info("projects-svc main http listening", "port", cfg.HTTPPort)
		if err := http.ListenAndServe(":"+cfg.HTTPPort, mainSrv.Handler()); err != nil {
			slog.Error("main http server failed", "error", err)
		}
	}()

	go func() {
		listener, err := net.Listen("tcp", "127.0.0.1:"+cfg.NotifyPort)
		if err != nil {
			slog.Error("notify listener bind failed", "error", err)
			return
		}
		slog.Info("projects-svc notify listener bound to loopback only", "port", cfg.NotifyPort)
		if err := notifySrv.Serve(listener); err != nil {
			slog.Error("notify http server failed", "error", err)
		}
	}()

	// Resume dispatching any queue that built up while projects-svc was down.
	dispatcher.TryDispatchNext(ctx)

	slog.Info("projects-svc started", "schema", cfg.Schema, "http_port", cfg.HTTPPort, "notify_port", cfg.NotifyPort)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("projects-svc shutting down")
}
