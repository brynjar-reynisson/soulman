package logmonitor

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/google/uuid"

	"soulman/common"
)

// Publisher is satisfied by *natspublish.Publisher. Declared here (not
// imported from natspublish) to avoid an import cycle — same rationale as
// every other channel package (watcher.Publisher, gmailwatcher.Publisher,
// sysmonitor.Publisher).
type Publisher interface {
	Publish(ctx context.Context, s *common.Stimulus) error
}

// logSuffix is the filename suffix every tracked file must end with;
// service is this suffix stripped from the filename.
const logSuffix = "-startup-err.log"

// Watcher tails every sibling service's *-startup-err.log file in logDir
// for new ERROR-level slog lines, publishing a Stimulus the first time a
// given (service, msg) pair is seen this process lifetime. See
// docs/superpowers/specs/2026-07-27-log-error-perception-design.md.
type Watcher struct {
	logDir            string
	publisher         Publisher
	checkpoint        *checkpoint
	dedup             *dedupState
	reconcileInterval time.Duration

	// mu serializes processFile across the fsnotify event-loop goroutine and
	// the periodic reconciliation goroutine, so the same file is never read
	// concurrently from two stale offsets at once.
	mu sync.Mutex

	fsw    *fsnotify.Watcher
	cancel context.CancelFunc
}

// New creates a Watcher tailing *-startup-err.log files in logDir,
// persisting its byte-offset checkpoint to checkpointPath. Mirrors
// watcher.New's constructor shape (fsnotify setup that can fail returns an
// error; everything else is deferred to Start).
func New(logDir string, publisher Publisher, checkpointPath string, reconcileInterval time.Duration) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("logmonitor: create fsnotify watcher: %w", err)
	}

	return &Watcher{
		logDir:            logDir,
		publisher:         publisher,
		checkpoint:        loadCheckpoint(checkpointPath),
		dedup:             newDedupState(),
		reconcileInterval: reconcileInterval,
		fsw:               fsw,
	}, nil
}

// Start adds an fsnotify watch on logDir (logging and continuing if it
// doesn't exist yet — retried automatically by the next reconciliation
// scan, mirroring watcher.Start's per-path tolerance), then launches the
// fsnotify event loop and the periodic reconciliation loop in background
// goroutines. It also runs one immediate reconciliation pass before
// returning, so files already present at startup are picked up without
// waiting a full interval.
func (w *Watcher) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel

	if err := w.fsw.Add(w.logDir); err != nil {
		slog.Warn("logmonitor: cannot watch log dir, will retry via reconciliation", "dir", w.logDir, "error", err)
	}

	go w.fsEventLoop(ctx)
	go w.reconcileLoop(ctx)

	w.reconcileAll(ctx)
}

// fsEventLoop drains fsnotify's Events/Errors channels and reconciles the
// specific file that changed on any Write event — instant reaction, backed
// by reconcileLoop's periodic full-directory scan as a safety net for any
// event fsnotify misses.
func (w *Watcher) fsEventLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if ev.Op&fsnotify.Write == 0 {
				continue
			}
			if !strings.HasSuffix(ev.Name, logSuffix) {
				continue
			}
			w.processFile(ctx, ev.Name)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			slog.Error("logmonitor: fsnotify error", "error", err)
		}
	}
}

// reconcileLoop runs a full-directory scan every reconcileInterval.
func (w *Watcher) reconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(w.reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.reconcileAll(ctx)
		}
	}
}

// reconcileAll globs logDir for every tracked file and processes each —
// catches files changed while perception-svc was down or any fsnotify
// event that was missed (a known OS-level gap on some network drives, per
// watcher's own precedent).
func (w *Watcher) reconcileAll(ctx context.Context) {
	matches, err := filepath.Glob(filepath.Join(w.logDir, "*"+logSuffix))
	if err != nil {
		slog.Error("logmonitor: glob log dir failed", "dir", w.logDir, "error", err)
		return
	}
	for _, path := range matches {
		w.processFile(ctx, path)
	}
}

// processFile reads path from its checkpointed offset to current EOF,
// splits into complete lines (a trailing partial line is left for the next
// read), parses and dedups each ERROR line, and advances the checkpoint
// after a successful read.
func (w *Watcher) processFile(ctx context.Context, path string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	filename := filepath.Base(path)
	service := strings.TrimSuffix(filename, logSuffix)

	info, err := os.Stat(path)
	if err != nil {
		slog.Error("logmonitor: stat failed, skipping this cycle", "path", path, "error", err)
		return
	}
	size := info.Size()

	_, hadEntry := w.checkpoint.offsetFor(filename)
	start := w.checkpoint.resolveStart(filename, size)
	if start >= size {
		// Nothing new relative to the checkpointed offset. If this is the
		// first time this file has been seen, that offset (the EOF baseline
		// resolveStart just computed) must still be persisted now — otherwise
		// the next call finds no checkpoint entry either, re-resolves "start"
		// against whatever the file's size has grown to by then, and silently
		// skips everything written in between.
		if !hadEntry {
			if err := w.checkpoint.mark(filename, start); err != nil {
				slog.Warn("logmonitor: checkpoint write failed", "path", path, "error", err)
			}
		}
		return // nothing new to read
	}

	f, err := os.Open(path)
	if err != nil {
		slog.Error("logmonitor: open failed, skipping this cycle", "path", path, "error", err)
		return
	}
	defer f.Close()

	if _, err := f.Seek(start, 0); err != nil {
		slog.Error("logmonitor: seek failed, skipping this cycle", "path", path, "error", err)
		return
	}

	reader := bufio.NewReader(f)
	offset := start
	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 && strings.HasSuffix(line, "\n") {
			offset += int64(len(line))
			w.handleLine(ctx, service, strings.TrimRight(line, "\n"))
		}
		if readErr != nil {
			break // EOF or read error: stop, leaving any trailing partial line for next time
		}
	}

	if err := w.checkpoint.mark(filename, offset); err != nil {
		slog.Warn("logmonitor: checkpoint write failed", "path", path, "error", err)
	}
}

// handleLine parses one line; if it's a new (service, msg) ERROR pair,
// builds and publishes a Stimulus. A publish failure is logged and the
// dedup key is deliberately left unmarked, so the same line is retried on
// the next matching read rather than being permanently swallowed.
func (w *Watcher) handleLine(ctx context.Context, service, line string) {
	parsed, ok := parseLine(line)
	if !ok {
		return
	}
	if w.dedup.seenBefore(service, parsed.Msg) {
		return
	}

	stimulus := buildStimulus(service, parsed.Msg, line, time.Now())
	if err := w.publisher.Publish(ctx, stimulus); err != nil {
		slog.Error("logmonitor: publish failed, will retry on next matching line", "service", service, "error", err)
		return
	}
	w.dedup.markSeen(service, parsed.Msg)
}

func (w *Watcher) Close() error {
	if w.cancel != nil {
		w.cancel()
	}
	return w.fsw.Close()
}

// buildStimulus constructs a Stimulus per the log-error perception design
// spec's Stimulus Construction table.
func buildStimulus(service, msg, rawLine string, now time.Time) *common.Stimulus {
	specific, _ := json.Marshal(struct {
		Service string `json:"service"`
		Msg     string `json:"msg"`
	}{Service: service, Msg: msg})

	id, err := uuid.NewV7()
	if err != nil {
		// Extremely unlikely (crypto/rand failure); fall back to a random v4
		// rather than crash the watcher over one reading.
		id = uuid.New()
	}

	return &common.Stimulus{
		StimulusID:    id.String(),
		SchemaVersion: 1,
		ReceivedAt:    now,
		OccurredAt:    &now,
		Channel:       "log-error",
		Source: common.Source{
			Identity:      "log-error",
			Authenticated: true,
			AuthMethod:    "system",
		},
		Content: common.Content{
			RawText:     rawLine,
			RawPayload:  json.RawMessage(`{}`),
			ContentType: "text",
			Attachments: []common.Attachment{},
		},
		ChannelMeta: common.ChannelMeta{
			MessageID:       computeMessageID(service, msg, now),
			ChannelSpecific: specific,
		},
		Hints: common.Hints{
			Priority: "critical",
			Tags:     []string{"system", "log-error", service},
		},
		Override: common.Override{
			IsOverride: false,
			Params:     json.RawMessage(`{}`),
		},
	}
}

// computeMessageID mirrors every other channel's dedup-key-for-the-wire
// convention: sha256(service + msg + occurred_at).
func computeMessageID(service, msg string, occurredAt time.Time) string {
	sum := sha256.Sum256([]byte(service + msg + occurredAt.Format(time.RFC3339)))
	return hex.EncodeToString(sum[:])
}
