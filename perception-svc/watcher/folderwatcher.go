package watcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fsnotify/fsnotify"
	"github.com/google/uuid"

	"soulman/common"
)

const (
	// maxInlineBytes is the attachment inline threshold from the spec: files
	// smaller than this and valid UTF-8 are inlined as raw_text; everything
	// else becomes a single attachment entry.
	maxInlineBytes = 1 << 20 // 1 MB

	// maxQueuedEvents bounds the in-memory fsnotify event queue, mirroring
	// Perception module.md's max_buffer_size default. Overflow is dropped;
	// the next reconciliation scan catches it instead.
	maxQueuedEvents = 100
)

// Publisher is satisfied by *natspublish.Publisher. Declared here (not
// imported from natspublish) to avoid an import cycle — watcher has no
// dependency on natspublish.
type Publisher interface {
	Publish(ctx context.Context, s *common.Stimulus) error
}

// Watcher watches a set of directories (top-level only, not recursive) for
// newly created files and publishes each as a Stimulus, backed by a
// checkpoint file so already-seen files aren't re-published.
type Watcher struct {
	paths             []string
	checkpoint        *Checkpoint
	publisher         Publisher
	reconcileInterval time.Duration

	fsw    *fsnotify.Watcher
	events chan fsnotify.Event
}

func New(paths []string, checkpoint *Checkpoint, publisher Publisher, reconcileInterval time.Duration) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("watcher: create fsnotify watcher: %w", err)
	}

	return &Watcher{
		paths:             paths,
		checkpoint:        checkpoint,
		publisher:         publisher,
		reconcileInterval: reconcileInterval,
		fsw:               fsw,
		events:            make(chan fsnotify.Event, maxQueuedEvents),
	}, nil
}

// Start adds an fsnotify watch for each configured directory (logging and
// skipping any that don't exist yet — retried automatically by the next
// reconciliation scan), then launches the fsnotify event loop and the
// periodic reconciliation loop in background goroutines. It also runs one
// immediate reconciliation pass before returning, so files already present
// at startup are picked up without waiting a full interval.
func (w *Watcher) Start(ctx context.Context) {
	for _, p := range w.paths {
		if err := w.fsw.Add(p); err != nil {
			slog.Warn("watcher: cannot watch path, will retry via reconciliation", "path", p, "error", err)
		}
	}

	go w.fsEventLoop(ctx)
	go w.processLoop(ctx)
	go w.reconcileLoop(ctx)

	w.reconcileAll(ctx)
}

// fsEventLoop drains fsnotify's Events/Errors channels and enqueues Create
// events onto the bounded internal queue, dropping (non-blocking) if full.
func (w *Watcher) fsEventLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if ev.Op&fsnotify.Create == 0 {
				continue
			}
			select {
			case w.events <- ev:
			default:
				slog.Warn("watcher: event queue full, dropping create event — reconciliation will catch it", "queue_size", maxQueuedEvents, "file", ev.Name)
			}
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			slog.Error("watcher: fsnotify error", "error", err)
		}
	}
}

// processLoop handles queued Create events one at a time.
func (w *Watcher) processLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-w.events:
			w.handleFile(ctx, filepath.Dir(ev.Name), filepath.Base(ev.Name))
		}
	}
}

// reconcileLoop runs a reconciliation scan every reconcileInterval.
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

// reconcileAll lists each watched directory and diffs its files against the
// checkpoint, catching files created while the service was down and files
// fsnotify missed (a known OS-level gap on some network drives).
func (w *Watcher) reconcileAll(ctx context.Context) {
	for _, dir := range w.paths {
		entries, err := os.ReadDir(dir)
		if err != nil {
			slog.Warn("watcher: reconcile cannot list directory, will retry next scan", "dir", dir, "error", err)
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			w.handleFile(ctx, dir, e.Name())
		}
	}
}

// handleFile reads filename in dir and, if it's new or changed since the
// last checkpoint, builds and publishes a Stimulus, then marks the
// checkpoint on success.
func (w *Watcher) handleFile(ctx context.Context, dir, filename string) {
	if isTempFilename(filename) {
		slog.Info("watcher: skipping temp file", "path", filepath.Join(dir, filename))
		return
	}

	fullPath := filepath.Join(dir, filename)

	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Info("watcher: file deleted before it could be read, skipping", "path", fullPath)
			return
		}
		slog.Warn("watcher: stat failed", "path", fullPath, "error", err)
		return
	}
	if info.IsDir() {
		return
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		slog.Warn("watcher: read failed", "path", fullPath, "error", err)
		return
	}

	// A zero-byte read usually means we raced a writer that hasn't put
	// content on disk yet (e.g. the fsnotify Create event fired the instant
	// the file was opened). Skip without checkpointing so a later pass —
	// another fsnotify event or the next reconciliation tick — retries once
	// content is actually there, instead of publishing a bogus empty
	// stimulus that thinking-svc's error-report rule can't distinguish from
	// a genuine binary attachment.
	if len(data) == 0 {
		slog.Info("watcher: file read as empty, will retry", "path", fullPath)
		return
	}

	hash := hashBytes(data)
	if !w.checkpoint.IsNew(dir, filename, hash) {
		return
	}

	mtime := info.ModTime().UTC()
	stimulus := buildStimulus(dir, filename, data, mtime)

	if err := w.publisher.Publish(ctx, stimulus); err != nil {
		slog.Error("watcher: publish failed, checkpoint left unset, will retry", "path", fullPath, "error", err)
		return
	}

	entry := CheckpointEntry{
		Hash:        hash,
		Mtime:       mtime.Format(time.RFC3339),
		PublishedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := w.checkpoint.Mark(dir, filename, entry); err != nil {
		slog.Warn("watcher: checkpoint write failed, may re-publish on restart", "path", fullPath, "error", err)
	}
}

func (w *Watcher) Close() error {
	return w.fsw.Close()
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// isTempFilename reports whether filename looks like the transient product
// of an atomic write (write to a temp name, then rename into place) rather
// than a finished file — e.g. "report.txt.tmp.42272.f44c9650e4f9", the
// pattern observed from real writers dropping files into the watched
// folder. Matching both a mid-name ".tmp." segment and a bare ".tmp" suffix
// covers writers that append a random/pid suffix and ones that don't.
func isTempFilename(filename string) bool {
	return strings.Contains(filename, ".tmp.") || strings.HasSuffix(filename, ".tmp")
}

// buildStimulus constructs a Stimulus per the perception-svc design spec's
// Stimulus Construction field mapping table.
func buildStimulus(watchedPath, filename string, data []byte, mtime time.Time) *common.Stimulus {
	isText := len(data) < maxInlineBytes && utf8.Valid(data)

	content := common.Content{RawPayload: json.RawMessage(`{}`)}
	if isText {
		content.RawText = string(data)
		content.ContentType = "text"
		content.Attachments = []common.Attachment{}
	} else {
		content.RawText = ""
		content.ContentType = "binary"
		content.Attachments = []common.Attachment{{
			Filename:  filename,
			MIMEType:  mimeType(filename),
			SizeBytes: int64(len(data)),
			URI:       filepath.Join(watchedPath, filename),
		}}
	}

	mtimeStr := mtime.Format(time.RFC3339)
	occurredAt := mtime
	id, err := uuid.NewV7()
	if err != nil {
		// Extremely unlikely (crypto/rand failure); fall back to a random v4
		// rather than crash the watcher over a single file.
		id = uuid.New()
	}

	return &common.Stimulus{
		StimulusID:    id.String(),
		SchemaVersion: 1,
		ReceivedAt:    time.Now().UTC(),
		OccurredAt:    &occurredAt,
		Channel:       "folder-watcher",
		Source: common.Source{
			Identity:      "folder-watcher",
			Authenticated: true,
			AuthMethod:    "system",
		},
		Content: content,
		ChannelMeta: common.ChannelMeta{
			MessageID:       computeMessageID(watchedPath, filename, mtimeStr),
			ChannelSpecific: json.RawMessage(fmt.Sprintf(`{"watched_path":%s}`, jsonString(watchedPath))),
		},
		Hints: common.Hints{
			Priority: "high",
			Tags:     []string{"error", "folder-watcher"},
		},
		Override: common.Override{
			IsOverride: false,
			Params:     json.RawMessage(`{}`),
		},
	}
}

// computeMessageID gives downstream consumers a stable dedup key, per spec:
// sha256(watched_path + filename + mtime).
func computeMessageID(watchedPath, filename, mtimeRFC3339 string) string {
	sum := sha256.Sum256([]byte(watchedPath + filename + mtimeRFC3339))
	return hex.EncodeToString(sum[:])
}

func mimeType(filename string) string {
	if t := mime.TypeByExtension(filepath.Ext(filename)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// jsonString safely encodes a Go string as a JSON string literal, used to
// build the channel_specific object without pulling in a struct just for
// one field.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
