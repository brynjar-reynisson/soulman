package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"soulman/projects-svc/dispatch"
	"soulman/projects-svc/httpserver"
	"soulman/projects-svc/store"
)

// fakeStore implements the subset of *store.Store methods the httpserver
// package calls directly (list/create/update/delete), independent of the
// dispatch.Store interface in Task 5.
type fakeStore struct {
	projects       []store.Project
	prompts        []store.Prompt
	createErr      error
	updateErr      error
	deleteErr      error
	createPromptErr error
	updateStateErr error
	nextID         int64
}

func (f *fakeStore) ListProjects(ctx context.Context) ([]store.Project, error) { return f.projects, nil }
func (f *fakeStore) CreateProject(ctx context.Context, p store.Project) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.projects = append(f.projects, p)
	return nil
}
func (f *fakeStore) UpdateProject(ctx context.Context, name, path string) error { return f.updateErr }
func (f *fakeStore) DeleteProject(ctx context.Context, name string) error       { return f.deleteErr }
func (f *fakeStore) ListPrompts(ctx context.Context) ([]store.Prompt, error)    { return f.prompts, nil }
func (f *fakeStore) CreatePrompt(ctx context.Context, projectName, taskName, promptText string) (store.Prompt, error) {
	if f.createPromptErr != nil {
		return store.Prompt{}, f.createPromptErr
	}
	f.nextID++
	p := store.Prompt{ID: f.nextID, ProjectName: projectName, TaskName: taskName, PromptText: promptText, State: store.StateNotStarted}
	f.prompts = append(f.prompts, p)
	return p, nil
}
func (f *fakeStore) UpdatePromptState(ctx context.Context, id int64, state string) error {
	return f.updateStateErr
}

// noopDispatchStore is a dispatch.Store that always reports "busy" (a
// prompt is CREATING_SPEC), so TryDispatchNext no-ops immediately without
// calling any other method. Used to build an inert *dispatch.Dispatcher
// for tests that don't exercise dispatch behavior — passing a nil Store
// interface instead (newNoopDispatcher()) is NOT safe: a nil-interface
// method call panics, and Task 7's /notify handler runs TryDispatchNext
// in an unrecovered goroutine, so that panic would crash the whole test
// binary rather than just fail one test. This type is defined once, here,
// and reused by Task 7's notify_test.go (same httpserver_test package —
// do not redefine it there).
type noopDispatchStore struct{}

func (noopDispatchStore) HasCreatingSpec(ctx context.Context) (bool, error) { return true, nil }
func (noopDispatchStore) OldestNotStarted(ctx context.Context) (*store.Prompt, error) {
	return nil, nil
}
func (noopDispatchStore) GetProject(ctx context.Context, name string) (store.Project, error) {
	return store.Project{}, nil
}
func (noopDispatchStore) MarkCreatingSpec(ctx context.Context, id int64) error { return nil }
func (noopDispatchStore) SetLaunchError(ctx context.Context, id int64, msg string) error {
	return nil
}

func newNoopDispatcher() *dispatch.Dispatcher {
	return dispatch.New(noopDispatchStore{}, func(store.Project, store.Prompt) error { return nil })
}

func TestListProjects_ReturnsJSON(t *testing.T) {
	fs := &fakeStore{projects: []store.Project{{Name: "demo", Path: `C:\demo`}}}
	srv := httpserver.New(fs, newNoopDispatcher())

	req := httptest.NewRequest(http.MethodGet, "/projects", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var got []store.Project
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].Name != "demo" {
		t.Errorf("got %+v, want one project named demo", got)
	}
}

func TestCreateProject_Success_Returns201(t *testing.T) {
	fs := &fakeStore{}
	srv := httpserver.New(fs, newNoopDispatcher())

	body, _ := json.Marshal(store.Project{Name: "demo", Path: `C:\demo`})
	req := httptest.NewRequest(http.MethodPost, "/projects", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateProject_AlreadyExists_Returns409(t *testing.T) {
	fs := &fakeStore{createErr: store.ErrAlreadyExists}
	srv := httpserver.New(fs, newNoopDispatcher())

	body, _ := json.Marshal(store.Project{Name: "demo", Path: `C:\demo`})
	req := httptest.NewRequest(http.MethodPost, "/projects", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestCreateProject_MissingFields_Returns400(t *testing.T) {
	fs := &fakeStore{}
	srv := httpserver.New(fs, newNoopDispatcher())

	body, _ := json.Marshal(store.Project{Name: "demo"})
	req := httptest.NewRequest(http.MethodPost, "/projects", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestDeleteProject_HasPrompts_Returns409(t *testing.T) {
	fs := &fakeStore{deleteErr: store.ErrHasPrompts}
	srv := httpserver.New(fs, newNoopDispatcher())

	req := httptest.NewRequest(http.MethodDelete, "/projects/demo", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeleteProject_NotFound_Returns404(t *testing.T) {
	fs := &fakeStore{deleteErr: store.ErrNotFound}
	srv := httpserver.New(fs, newNoopDispatcher())

	req := httptest.NewRequest(http.MethodDelete, "/projects/missing", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestCreatePrompt_Success_Returns201(t *testing.T) {
	fs := &fakeStore{}
	srv := httpserver.New(fs, newNoopDispatcher())

	body, _ := json.Marshal(map[string]string{"project_name": "demo", "task_name": "task", "prompt_text": "do it"})
	req := httptest.NewRequest(http.MethodPost, "/prompts", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreatePrompt_UnknownProject_Returns400(t *testing.T) {
	fs := &fakeStore{createPromptErr: store.ErrNotFound}
	srv := httpserver.New(fs, newNoopDispatcher())

	body, _ := json.Marshal(map[string]string{"project_name": "missing", "task_name": "task", "prompt_text": "do it"})
	req := httptest.NewRequest(http.MethodPost, "/prompts", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestUpdatePromptState_InvalidState_Returns400(t *testing.T) {
	fs := &fakeStore{}
	srv := httpserver.New(fs, newNoopDispatcher())

	body, _ := json.Marshal(map[string]string{"state": "NOT_A_REAL_STATE"})
	req := httptest.NewRequest(http.MethodPut, "/prompts/1", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestUpdatePromptState_ValidState_Returns204(t *testing.T) {
	fs := &fakeStore{}
	srv := httpserver.New(fs, newNoopDispatcher())

	body, _ := json.Marshal(map[string]string{"state": store.StateNotStarted})
	req := httptest.NewRequest(http.MethodPut, "/prompts/1", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
}
