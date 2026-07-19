package scheduler

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"soulman/action-svc/feign"
	"soulman/action-svc/notify"
	"soulman/action-svc/report"
	"soulman/common"
)

// OutcomePublisher is satisfied by *natsclient.Publisher. Defined here (not
// in natsclient) so this package doesn't need to import natsclient.
type OutcomePublisher interface {
	PublishOutcome(rec common.OutcomeRecord) error
}

type Scheduler struct {
	root      string
	sendTime  string
	notifier  notify.Notifier
	publisher OutcomePublisher
	gate      *feign.Gate
	stop      chan struct{}

	// Overridable for tests: Now controls "current time" (avoids waiting for
	// a real clock), BackoffBase controls the retry delay (avoids a slow test).
	Now         func() time.Time
	BackoffBase time.Duration
}

func New(root, sendTime string, notifier notify.Notifier, publisher OutcomePublisher, gate *feign.Gate) *Scheduler {
	return &Scheduler{
		root:        root,
		sendTime:    sendTime,
		notifier:    notifier,
		publisher:   publisher,
		gate:        gate,
		stop:        make(chan struct{}),
		Now:         time.Now,
		BackoffBase: 1 * time.Second,
	}
}

func (s *Scheduler) Start() {
	go s.loop()
}

func (s *Scheduler) Stop() {
	close(s.stop)
}

func (s *Scheduler) loop() {
	for {
		wait := time.Until(s.nextRun(s.Now()))
		select {
		case <-time.After(wait):
			s.RunOnce()
		case <-s.stop:
			return
		}
	}
}

func (s *Scheduler) nextRun(from time.Time) time.Time {
	hh, mm := parseSendTime(s.sendTime)
	next := time.Date(from.Year(), from.Month(), from.Day(), hh, mm, 0, 0, from.Location())
	if !next.After(from) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func parseSendTime(s string) (int, int) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 10, 0
	}
	hh, err1 := strconv.Atoi(parts[0])
	mm, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 10, 0
	}
	return hh, mm
}

// RunOnce performs a single check-and-send cycle: read yesterday's report,
// skip if missing/empty/whitespace-only, otherwise send via the Notifier
// with retry, then log the outcome. The report file is never modified or
// deleted here — only report.Read is used.
func (s *Scheduler) RunOnce() {
	yesterday := s.Now().AddDate(0, 0, -1)

	content, err := report.Read(s.root, yesterday)
	if err != nil {
		log.Printf("scheduler: read report for %s failed, will retry tomorrow: %v", yesterday.Format("2006-01-02"), err)
		return
	}
	if strings.TrimSpace(content) == "" {
		log.Printf("scheduler: report for %s empty or missing, nothing to send", yesterday.Format("2006-01-02"))
		return
	}

	err = s.sendWithRetry(content)
	status := "success"
	var summary string
	switch {
	case err != nil:
		status = "failed"
		summary = fmt.Sprintf("Daily report delivery failed: %v", err)
		log.Printf("scheduler: notifier send failed after 3 attempts: %v", err)
	case s.gate.Enabled():
		summary = "Daily report delivery feigned"
	default:
		summary = "Daily report delivered"
	}

	if s.publisher == nil {
		return
	}

	rec := common.OutcomeRecord{
		ActionType: "daily_report_delivery",
		Status:     status,
		TaskID:     "",
		OccurredAt: s.Now(),
		Summary:    summary,
		Decision:   "daily_report_delivery",
		Tags:       []string{"report", "cron"},
	}
	if pubErr := s.publisher.PublishOutcome(rec); pubErr != nil {
		log.Printf("scheduler: outcome publish failed: %v", pubErr)
	}
}

func (s *Scheduler) sendWithRetry(content string) error {
	var err error
	backoff := s.BackoffBase
	for attempt := 1; attempt <= 3; attempt++ {
		err = s.notifier.Send(content)
		if err == nil {
			return nil
		}
		log.Printf("scheduler: notifier send attempt %d/3 failed: %v", attempt, err)
		if attempt < 3 {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	return err
}
