package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"soulman/thinking-svc/config"
	"soulman/thinking-svc/httpserver"
	"soulman/thinking-svc/llm"
	"soulman/common"
	"soulman/thinking-svc/natsclient"
	"soulman/thinking-svc/rules"
)

// stimulusHandler wires rules.Process's output into the NATS publisher. It
// implements natsclient.Handler. Kept in main so natsclient never needs to
// import rules (see the plan's File Structure note on dependency flow).
type stimulusHandler struct {
	client    llm.Client
	publisher *natsclient.Publisher
}

func (h *stimulusHandler) Handle(ctx context.Context, s *common.Stimulus) error {
	req, err := rules.Process(ctx, s, h.client)
	if err != nil {
		return fmt.Errorf("rule handling failed for %s: %w", s.StimulusID, err)
	}
	if req == nil {
		return nil // no rule matched; no-op per the design spec
	}
	if err := h.publisher.Publish(ctx, req); err != nil {
		return fmt.Errorf("publish action request for %s: %w", s.StimulusID, err)
	}
	return nil
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if cfg.DeepSeekAPIKey == "" {
		slog.Warn("DEEPSEEK_API_KEY not set — summarization calls will fail and fall back to deterministic summaries")
	}
	if cfg.SchoolEnabled && len(cfg.SchoolSenderDomains) > 0 {
		rules.Registry = append([]rules.Rule{rules.NewSchoolEmailRule(cfg.SchoolSenderDomains, cfg.SchoolRelevantGrades)}, rules.Registry...)
		slog.Info("school-email rule enabled", "sender_domains", cfg.SchoolSenderDomains, "relevant_grades", cfg.SchoolRelevantGrades)
	} else {
		slog.Info("school-email rule disabled", "enabled", cfg.SchoolEnabled, "sender_domains_count", len(cfg.SchoolSenderDomains))
	}

	summarizer := llm.NewDeepSeekClient(
		cfg.DeepSeekAPIKey,
		cfg.DeepSeekBaseURL,
		cfg.DeepSeekModel,
		time.Duration(cfg.DeepSeekTimeoutSeconds)*time.Second,
	)

	publisher, err := natsclient.NewPublisher(ctx, cfg.NATSURL, cfg.ThinkingRequestSubject)
	if err != nil {
		slog.Error("nats publisher init failed", "error", err)
		os.Exit(1)
	}
	defer publisher.Close()

	handler := &stimulusHandler{client: summarizer, publisher: publisher}

	consumer, err := natsclient.NewConsumer(cfg.NATSURL, cfg.ConsumerName, cfg.StimulusSubject, handler)
	if err != nil {
		slog.Error("nats consumer init failed", "error", err)
		os.Exit(1)
	}
	defer consumer.Close()

	if err := consumer.Start(ctx); err != nil {
		slog.Error("nats consumer start failed", "error", err)
		os.Exit(1)
	}

	srv := httpserver.New(cfg.HTTPPort)
	go func() {
		slog.Info("http listening", "port", cfg.HTTPPort)
		if err := srv.Start(); err != nil {
			slog.Error("http server failed", "error", err)
		}
	}()

	slog.Info("thinking-svc started", "nats_url", cfg.NATSURL, "http_port", cfg.HTTPPort, "deepseek_model", cfg.DeepSeekModel)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("thinking-svc shutting down")
}
