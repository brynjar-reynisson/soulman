package main

import (
	"context"
	"fmt"
	"log"
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
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if cfg.DeepSeekAPIKey == "" {
		log.Printf("WARNING: DEEPSEEK_API_KEY not set — summarization calls will fail and fall back to deterministic summaries")
	}
	summarizer := llm.NewDeepSeekClient(
		cfg.DeepSeekAPIKey,
		cfg.DeepSeekBaseURL,
		cfg.DeepSeekModel,
		time.Duration(cfg.DeepSeekTimeoutSeconds)*time.Second,
	)

	publisher, err := natsclient.NewPublisher(ctx, cfg.NATSURL, cfg.ThinkingRequestSubject)
	if err != nil {
		log.Fatalf("nats publisher: %v", err)
	}
	defer publisher.Close()

	handler := &stimulusHandler{client: summarizer, publisher: publisher}

	consumer, err := natsclient.NewConsumer(cfg.NATSURL, cfg.ConsumerName, cfg.StimulusSubject, handler)
	if err != nil {
		log.Fatalf("nats consumer: %v", err)
	}
	defer consumer.Close()

	if err := consumer.Start(ctx); err != nil {
		log.Fatalf("nats consumer start: %v", err)
	}

	srv := httpserver.New(cfg.HTTPPort)
	go func() {
		log.Printf("HTTP listening on :%s", cfg.HTTPPort)
		if err := srv.Start(); err != nil {
			log.Printf("http: %v", err)
		}
	}()

	log.Printf("thinking-svc started (NATS=%s, HTTP=:%s, DeepSeek model=%s)",
		cfg.NATSURL, cfg.HTTPPort, cfg.DeepSeekModel)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Printf("thinking-svc shutting down")
}
