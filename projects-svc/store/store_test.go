package store_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"soulman/projects-svc/store"
)

// testSchema is a dedicated schema for these tests, separate from
// projects_dev (the real dev deployment's schema). Several tests below
// assert global conditions (no other CREATING_SPEC prompt exists anywhere,
// a freshly created prompt is the oldest NOT_STARTED row anywhere, etc.)
// that would start failing the moment real dev data exists if this ran
// against projects_dev. Apply the DDL to this schema before running these
// tests locally:
//
//	psql "postgres://postgres:postgres@localhost:54322/postgres" -v schema=projects_test -f docs/superpowers/specs/sql/2026-09-04-projects-tables.sql
const testSchema = "projects_test"

func testStore(t *testing.T) *store.Store {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:54322/postgres"
	}
	ctx := context.Background()
	s, err := store.New(ctx, dbURL, testSchema)
	if err != nil {
		t.Skipf("postgres not available (%v) — set DATABASE_URL to run DB tests", err)
	}
	t.Cleanup(s.Close)
	return s
}

func uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func cleanupProject(t *testing.T, s *store.Store, name string) {
	t.Cleanup(func() {
		s.ExecCleanup(context.Background(), "DELETE FROM "+testSchema+".prompt WHERE project_name = $1", name)
		s.ExecCleanup(context.Background(), "DELETE FROM "+testSchema+".project WHERE name = $1", name)
	})
}

func TestStore_CreateAndListProjects(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	name := uniqueName("proj")
	cleanupProject(t, s, name)

	if err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp\` + name}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	projects, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	found := false
	for _, p := range projects {
		if p.Name == name {
			found = true
		}
	}
	if !found {
		t.Errorf("ListProjects did not include created project %q", name)
	}
}

func TestStore_CreateProject_DuplicateName_ReturnsErrAlreadyExists(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	name := uniqueName("dup")
	cleanupProject(t, s, name)

	if err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp\a`}); err != nil {
		t.Fatalf("first CreateProject: %v", err)
	}
	err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp\b`})
	if err != store.ErrAlreadyExists {
		t.Fatalf("second CreateProject error = %v, want ErrAlreadyExists", err)
	}
}

func TestStore_UpdateProject_NotFound_ReturnsErrNotFound(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	err := s.UpdateProject(ctx, uniqueName("missing"), `C:\tmp`)
	if err != store.ErrNotFound {
		t.Fatalf("UpdateProject error = %v, want ErrNotFound", err)
	}
}

func TestStore_DeleteProject_WithPrompts_ReturnsErrHasPrompts(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	name := uniqueName("hasprompts")
	cleanupProject(t, s, name)

	if err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp`}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := s.CreatePrompt(ctx, name, "task", "do the thing"); err != nil {
		t.Fatalf("CreatePrompt: %v", err)
	}

	err := s.DeleteProject(ctx, name)
	if err != store.ErrHasPrompts {
		t.Fatalf("DeleteProject error = %v, want ErrHasPrompts", err)
	}
}

func TestStore_DeleteProject_NoPrompts_Succeeds(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	name := uniqueName("empty")

	if err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp`}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.DeleteProject(ctx, name); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := s.GetProject(ctx, name); err != store.ErrNotFound {
		t.Fatalf("GetProject after delete = %v, want ErrNotFound", err)
	}
}

func TestStore_CreatePrompt_UnknownProject_ReturnsErrNotFound(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	_, err := s.CreatePrompt(ctx, uniqueName("noproject"), "task", "text")
	if err != store.ErrNotFound {
		t.Fatalf("CreatePrompt error = %v, want ErrNotFound", err)
	}
}

func TestStore_CreatePrompt_DefaultsToNotStarted(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	name := uniqueName("proj")
	cleanupProject(t, s, name)

	if err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp`}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	p, err := s.CreatePrompt(ctx, name, "task", "text")
	if err != nil {
		t.Fatalf("CreatePrompt: %v", err)
	}
	if p.State != store.StateNotStarted {
		t.Errorf("State = %q, want %q", p.State, store.StateNotStarted)
	}
	if p.LastLaunchError != nil {
		t.Errorf("LastLaunchError = %v, want nil", p.LastLaunchError)
	}
	if p.ImplementationStartedAt != nil {
		t.Errorf("ImplementationStartedAt = %v, want nil", p.ImplementationStartedAt)
	}
	if p.DoneAt != nil {
		t.Errorf("DoneAt = %v, want nil", p.DoneAt)
	}
}

func TestStore_UpdatePromptState_StampsImplementationStartedAtAndDoneAt(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	name := uniqueName("proj")
	cleanupProject(t, s, name)

	if err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp`}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	p, err := s.CreatePrompt(ctx, name, "task", "text")
	if err != nil {
		t.Fatalf("CreatePrompt: %v", err)
	}

	find := func() store.Prompt {
		t.Helper()
		prompts, err := s.ListPrompts(ctx)
		if err != nil {
			t.Fatalf("ListPrompts: %v", err)
		}
		for _, got := range prompts {
			if got.ID == p.ID {
				return got
			}
		}
		t.Fatalf("prompt %d not found after ListPrompts", p.ID)
		return store.Prompt{}
	}

	if err := s.UpdatePromptState(ctx, p.ID, store.StateImplementing); err != nil {
		t.Fatalf("UpdatePromptState(IMPLEMENTING): %v", err)
	}
	got := find()
	if got.ImplementationStartedAt == nil {
		t.Error("ImplementationStartedAt = nil after transitioning to IMPLEMENTING, want a timestamp")
	}
	if got.DoneAt != nil {
		t.Errorf("DoneAt = %v after transitioning to IMPLEMENTING, want nil", got.DoneAt)
	}

	if err := s.UpdatePromptState(ctx, p.ID, store.StateDone); err != nil {
		t.Fatalf("UpdatePromptState(DONE): %v", err)
	}
	got = find()
	if got.ImplementationStartedAt == nil {
		t.Error("ImplementationStartedAt = nil after transitioning to DONE, want it to remain set from the earlier IMPLEMENTING transition")
	}
	if got.DoneAt == nil {
		t.Error("DoneAt = nil after transitioning to DONE, want a timestamp")
	}
}

func TestStore_MarkCreatingSpecAndHasCreatingSpec(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	name := uniqueName("proj")
	cleanupProject(t, s, name)

	if err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp`}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	p, err := s.CreatePrompt(ctx, name, "task", "text")
	if err != nil {
		t.Fatalf("CreatePrompt: %v", err)
	}

	if has, err := s.HasCreatingSpec(ctx); err != nil || has {
		t.Fatalf("HasCreatingSpec before mark = (%v, %v), want (false, nil)", has, err)
	}

	if err := s.MarkCreatingSpec(ctx, p.ID); err != nil {
		t.Fatalf("MarkCreatingSpec: %v", err)
	}

	if has, err := s.HasCreatingSpec(ctx); err != nil || !has {
		t.Fatalf("HasCreatingSpec after mark = (%v, %v), want (true, nil)", has, err)
	}
}

func TestStore_OldestNotStarted_PicksLowestID(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	name := uniqueName("proj")
	cleanupProject(t, s, name)

	if err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp`}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	first, err := s.CreatePrompt(ctx, name, "first", "text")
	if err != nil {
		t.Fatalf("CreatePrompt first: %v", err)
	}
	if _, err := s.CreatePrompt(ctx, name, "second", "text"); err != nil {
		t.Fatalf("CreatePrompt second: %v", err)
	}

	oldest, err := s.OldestNotStarted(ctx)
	if err != nil {
		t.Fatalf("OldestNotStarted: %v", err)
	}
	if oldest == nil || oldest.ID != first.ID {
		t.Fatalf("OldestNotStarted = %+v, want ID %d", oldest, first.ID)
	}
}

func TestStore_OldestNotStarted_NoneReturnsNil(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	name := uniqueName("proj")
	cleanupProject(t, s, name)

	if err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp`}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	p, err := s.CreatePrompt(ctx, name, "task", "text")
	if err != nil {
		t.Fatalf("CreatePrompt: %v", err)
	}
	if err := s.UpdatePromptState(ctx, p.ID, store.StateDone); err != nil {
		t.Fatalf("UpdatePromptState: %v", err)
	}

	oldest, err := s.OldestNotStarted(ctx)
	if err != nil {
		t.Fatalf("OldestNotStarted: %v", err)
	}
	if oldest != nil {
		t.Errorf("OldestNotStarted = %+v, want nil", oldest)
	}
}

func TestStore_SetLaunchError_ThenMarkCreatingSpecClearsIt(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	name := uniqueName("proj")
	cleanupProject(t, s, name)

	if err := s.CreateProject(ctx, store.Project{Name: name, Path: `C:\tmp`}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	p, err := s.CreatePrompt(ctx, name, "task", "text")
	if err != nil {
		t.Fatalf("CreatePrompt: %v", err)
	}

	if err := s.SetLaunchError(ctx, p.ID, "boom"); err != nil {
		t.Fatalf("SetLaunchError: %v", err)
	}
	prompts, err := s.ListPrompts(ctx)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	var got *store.Prompt
	for i := range prompts {
		if prompts[i].ID == p.ID {
			got = &prompts[i]
		}
	}
	if got == nil || got.LastLaunchError == nil || *got.LastLaunchError != "boom" {
		t.Fatalf("prompt after SetLaunchError = %+v, want LastLaunchError=boom", got)
	}

	if err := s.MarkCreatingSpec(ctx, p.ID); err != nil {
		t.Fatalf("MarkCreatingSpec: %v", err)
	}
	prompts, err = s.ListPrompts(ctx)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	for i := range prompts {
		if prompts[i].ID == p.ID {
			got = &prompts[i]
		}
	}
	if got.LastLaunchError != nil {
		t.Errorf("LastLaunchError after MarkCreatingSpec = %v, want nil", got.LastLaunchError)
	}
	if got.State != store.StateCreatingSpec {
		t.Errorf("State = %q, want %q", got.State, store.StateCreatingSpec)
	}
}

// Regression test for a live-prod incident (2026-09-04): ListProjects and
// ListPrompts used `var out []T` (a nil slice) as their accumulator, which
// encoding/json marshals as the JSON literal `null` rather than `[]` when
// no rows match. The frontend's project-select dropdown did an unguarded
// `projects.map(...)` assuming an array, so a `null` response threw and
// React unmounted the whole page to a blank screen — reproducible simply
// by opening the Projects page against a freshly deployed, empty database.
func TestStore_ListProjects_EmptyReturnsEmptySliceNotNil(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	s.ExecCleanup(ctx, "DELETE FROM "+testSchema+".prompt")
	s.ExecCleanup(ctx, "DELETE FROM "+testSchema+".project")

	projects, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if projects == nil {
		t.Error("ListProjects returned nil, want a non-nil empty slice (JSON must encode as [] not null)")
	}
	if len(projects) != 0 {
		t.Errorf("ListProjects returned %d projects, want 0 (schema was just cleared)", len(projects))
	}
}

func TestStore_ListPrompts_EmptyReturnsEmptySliceNotNil(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	s.ExecCleanup(ctx, "DELETE FROM "+testSchema+".prompt")
	s.ExecCleanup(ctx, "DELETE FROM "+testSchema+".project")

	prompts, err := s.ListPrompts(ctx)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	if prompts == nil {
		t.Error("ListPrompts returned nil, want a non-nil empty slice (JSON must encode as [] not null)")
	}
	if len(prompts) != 0 {
		t.Errorf("ListPrompts returned %d prompts, want 0 (schema was just cleared)", len(prompts))
	}
}
