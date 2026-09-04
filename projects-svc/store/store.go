// Package store is projects-svc's Postgres access layer for the project
// and prompt tables. Mirrors memory-svc/storage's schema-interpolation
// pattern (see memory-svc/storage/postgres.go) — the schema name is
// selected via fmt.Sprintf, not a bound parameter, since Postgres cannot
// parameterize identifiers.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	StateNotStarted   = "NOT_STARTED"
	StateCreatingSpec = "CREATING_SPEC"
	StateImplementing = "IMPLEMENTING"
	StateDone         = "DONE"
)

var (
	ErrNotFound      = errors.New("store: not found")
	ErrAlreadyExists = errors.New("store: already exists")
	ErrHasPrompts    = errors.New("store: project has prompts")
)

const foreignKeyViolation = "23503"

type Project struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type Prompt struct {
	ID              int64      `json:"id"`
	ProjectName     string     `json:"project_name"`
	TaskName        string     `json:"task_name"`
	PromptText      string     `json:"prompt_text"`
	State           string     `json:"state"`
	LastLaunchError *string    `json:"last_launch_error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	// ImplementationStartedAt/DoneAt are diagnostic-only — not surfaced in
	// the web UI (see docs/superpowers/specs/2026-09-04-projects-tool-design.md),
	// set by UpdatePromptState whenever a prompt transitions to
	// IMPLEMENTING/DONE respectively, regardless of whether that transition
	// came from the /notify callback or a manual state edit.
	ImplementationStartedAt *time.Time `json:"implementation_started_at,omitempty"`
	DoneAt                  *time.Time `json:"done_at,omitempty"`
}

type Store struct {
	pool   *pgxpool.Pool
	schema string
}

func New(ctx context.Context, connStr, schema string) (*Store, error) {
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{pool: pool, schema: schema}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT name, path FROM %s.project ORDER BY name`, s.schema))
	if err != nil {
		return nil, fmt.Errorf("store: list projects: %w", err)
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.Name, &p.Path); err != nil {
			return nil, fmt.Errorf("store: scan project: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CreateProject(ctx context.Context, p Project) error {
	tag, err := s.pool.Exec(ctx, fmt.Sprintf(
		`INSERT INTO %s.project (name, path) VALUES ($1, $2) ON CONFLICT (name) DO NOTHING`, s.schema),
		p.Name, p.Path)
	if err != nil {
		return fmt.Errorf("store: create project: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAlreadyExists
	}
	return nil
}

func (s *Store) UpdateProject(ctx context.Context, name, path string) error {
	tag, err := s.pool.Exec(ctx, fmt.Sprintf(
		`UPDATE %s.project SET path = $1 WHERE name = $2`, s.schema), path, name)
	if err != nil {
		return fmt.Errorf("store: update project: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteProject(ctx context.Context, name string) error {
	tag, err := s.pool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s.project WHERE name = $1`, s.schema), name)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
			return ErrHasPrompts
		}
		return fmt.Errorf("store: delete project: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetProject(ctx context.Context, name string) (Project, error) {
	var p Project
	err := s.pool.QueryRow(ctx, fmt.Sprintf(`SELECT name, path FROM %s.project WHERE name = $1`, s.schema), name).
		Scan(&p.Name, &p.Path)
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("store: get project: %w", err)
	}
	return p, nil
}

func (s *Store) ListPrompts(ctx context.Context) ([]Prompt, error) {
	rows, err := s.pool.Query(ctx, fmt.Sprintf(
		`SELECT id, project_name, task_name, prompt_text, state, last_launch_error, created_at, implementation_started_at, done_at
		 FROM %s.prompt ORDER BY id`, s.schema))
	if err != nil {
		return nil, fmt.Errorf("store: list prompts: %w", err)
	}
	defer rows.Close()
	out := []Prompt{}
	for rows.Next() {
		var p Prompt
		if err := rows.Scan(&p.ID, &p.ProjectName, &p.TaskName, &p.PromptText, &p.State, &p.LastLaunchError, &p.CreatedAt, &p.ImplementationStartedAt, &p.DoneAt); err != nil {
			return nil, fmt.Errorf("store: scan prompt: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CreatePrompt(ctx context.Context, projectName, taskName, promptText string) (Prompt, error) {
	var p Prompt
	err := s.pool.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO %s.prompt (project_name, task_name, prompt_text)
		VALUES ($1, $2, $3)
		RETURNING id, project_name, task_name, prompt_text, state, last_launch_error, created_at, implementation_started_at, done_at
	`, s.schema), projectName, taskName, promptText).
		Scan(&p.ID, &p.ProjectName, &p.TaskName, &p.PromptText, &p.State, &p.LastLaunchError, &p.CreatedAt, &p.ImplementationStartedAt, &p.DoneAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
			return Prompt{}, ErrNotFound
		}
		return Prompt{}, fmt.Errorf("store: create prompt: %w", err)
	}
	return p, nil
}

// UpdatePromptState sets state and, as a side effect, stamps
// implementation_started_at/done_at with now() whenever state is
// IMPLEMENTING/DONE respectively — the single point both the /notify
// callback and a manual UI state edit funnel through, so both paths get
// these diagnostic timestamps for free. Re-entering the same state again
// (e.g. a reset-then-relaunch) overwrites the timestamp with the latest
// occurrence rather than preserving the first.
func (s *Store) UpdatePromptState(ctx context.Context, id int64, state string) error {
	tag, err := s.pool.Exec(ctx, fmt.Sprintf(`
		UPDATE %s.prompt
		SET state = $1,
		    implementation_started_at = CASE WHEN $1 = '%s' THEN now() ELSE implementation_started_at END,
		    done_at = CASE WHEN $1 = '%s' THEN now() ELSE done_at END
		WHERE id = $2
	`, s.schema, StateImplementing, StateDone), state, id)
	if err != nil {
		return fmt.Errorf("store: update prompt state: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetLaunchError(ctx context.Context, id int64, msg string) error {
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`UPDATE %s.prompt SET last_launch_error = $1 WHERE id = $2`, s.schema), msg, id)
	if err != nil {
		return fmt.Errorf("store: set launch error: %w", err)
	}
	return nil
}

func (s *Store) MarkCreatingSpec(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, fmt.Sprintf(
		`UPDATE %s.prompt SET state = $1, last_launch_error = NULL WHERE id = $2`, s.schema),
		StateCreatingSpec, id)
	if err != nil {
		return fmt.Errorf("store: mark creating_spec: %w", err)
	}
	return nil
}

func (s *Store) HasCreatingSpec(ctx context.Context) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, fmt.Sprintf(
		`SELECT EXISTS(SELECT 1 FROM %s.prompt WHERE state = $1)`, s.schema), StateCreatingSpec).
		Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("store: has creating_spec: %w", err)
	}
	return exists, nil
}

func (s *Store) OldestNotStarted(ctx context.Context) (*Prompt, error) {
	var p Prompt
	err := s.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT id, project_name, task_name, prompt_text, state, last_launch_error, created_at, implementation_started_at, done_at
		FROM %s.prompt WHERE state = $1 ORDER BY id LIMIT 1
	`, s.schema), StateNotStarted).
		Scan(&p.ID, &p.ProjectName, &p.TaskName, &p.PromptText, &p.State, &p.LastLaunchError, &p.CreatedAt, &p.ImplementationStartedAt, &p.DoneAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: oldest not_started: %w", err)
	}
	return &p, nil
}

// ExecCleanup runs an arbitrary SQL statement — used only by tests for cleanup.
func (s *Store) ExecCleanup(ctx context.Context, sql string, args ...interface{}) {
	s.pool.Exec(ctx, sql, args...)
}
