// Package dispatch enforces "at most one prompt CREATING_SPEC at a time,
// globally" — see docs/superpowers/specs/2026-09-04-projects-tool-design.md's
// "Backend API & queue orchestration" section. Safe for concurrent calls
// from multiple HTTP handlers because exactly one projects-svc process
// runs per environment and TryDispatchNext is guarded by a single
// in-process mutex — no DB-level locking is needed.
package dispatch

import (
	"context"
	"log/slog"
	"sync"

	"soulman/projects-svc/store"
)

// Store is the subset of *store.Store this package needs — kept as a
// small local interface (rather than depending on the concrete type) so
// tests can supply a fake without touching Postgres.
type Store interface {
	HasCreatingSpec(ctx context.Context) (bool, error)
	OldestNotStarted(ctx context.Context) (*store.Prompt, error)
	GetProject(ctx context.Context, name string) (store.Project, error)
	MarkCreatingSpec(ctx context.Context, id int64) error
	SetLaunchError(ctx context.Context, id int64, msg string) error
}

// LaunchFunc starts a Claude Code session for prompt, rooted at project's
// path. In production this is an adapter around launcher.Launch (see
// projects-svc/main.go); tests supply a fake.
type LaunchFunc func(project store.Project, prompt store.Prompt) error

type Dispatcher struct {
	mu     sync.Mutex
	store  Store
	launch LaunchFunc
}

func New(st Store, launch LaunchFunc) *Dispatcher {
	return &Dispatcher{store: st, launch: launch}
}

// TryDispatchNext launches the oldest NOT_STARTED prompt if, and only if,
// no prompt is currently CREATING_SPEC. Errors from the store are logged
// and otherwise swallowed — this is called opportunistically after
// several different events (see callers), and none of them should fail
// just because a dispatch attempt couldn't proceed.
func (d *Dispatcher) TryDispatchNext(ctx context.Context) {
	d.mu.Lock()
	defer d.mu.Unlock()

	busy, err := d.store.HasCreatingSpec(ctx)
	if err != nil {
		slog.Error("dispatch: check creating_spec failed", "error", err)
		return
	}
	if busy {
		return
	}

	prompt, err := d.store.OldestNotStarted(ctx)
	if err != nil {
		slog.Error("dispatch: query oldest not_started failed", "error", err)
		return
	}
	if prompt == nil {
		return
	}

	project, err := d.store.GetProject(ctx, prompt.ProjectName)
	if err != nil {
		slog.Error("dispatch: load project failed", "project", prompt.ProjectName, "error", err)
		return
	}

	if err := d.launch(project, *prompt); err != nil {
		slog.Warn("dispatch: launch failed, prompt stays NOT_STARTED", "prompt_id", prompt.ID, "error", err)
		if serr := d.store.SetLaunchError(ctx, prompt.ID, err.Error()); serr != nil {
			slog.Error("dispatch: recording launch error failed", "prompt_id", prompt.ID, "error", serr)
		}
		return
	}

	if err := d.store.MarkCreatingSpec(ctx, prompt.ID); err != nil {
		slog.Error("dispatch: marking CREATING_SPEC failed after successful launch", "prompt_id", prompt.ID, "error", err)
	}
}
