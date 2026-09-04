package dispatch_test

import (
	"context"
	"errors"
	"testing"

	"soulman/projects-svc/dispatch"
	"soulman/projects-svc/store"
)

type fakeStore struct {
	hasCreatingSpec bool
	oldest          *store.Prompt
	projects        map[string]store.Project
	markedID        int64
	launchErrorID   int64
	launchErrorMsg  string
	hasCreatingSpecErr, oldestErr, getProjectErr, markErr, setErrErr error
}

func (f *fakeStore) HasCreatingSpec(ctx context.Context) (bool, error) {
	return f.hasCreatingSpec, f.hasCreatingSpecErr
}
func (f *fakeStore) OldestNotStarted(ctx context.Context) (*store.Prompt, error) {
	return f.oldest, f.oldestErr
}
func (f *fakeStore) GetProject(ctx context.Context, name string) (store.Project, error) {
	if f.getProjectErr != nil {
		return store.Project{}, f.getProjectErr
	}
	return f.projects[name], nil
}
func (f *fakeStore) MarkCreatingSpec(ctx context.Context, id int64) error {
	f.markedID = id
	return f.markErr
}
func (f *fakeStore) SetLaunchError(ctx context.Context, id int64, msg string) error {
	f.launchErrorID = id
	f.launchErrorMsg = msg
	return f.setErrErr
}

func TestTryDispatchNext_NoOpWhenAlreadyCreatingSpec(t *testing.T) {
	fs := &fakeStore{hasCreatingSpec: true}
	launchCalled := false
	d := dispatch.New(fs, func(store.Project, store.Prompt) error {
		launchCalled = true
		return nil
	})

	d.TryDispatchNext(context.Background())

	if launchCalled {
		t.Error("launch should not be called when a prompt is already CREATING_SPEC")
	}
}

func TestTryDispatchNext_NoOpWhenNothingQueued(t *testing.T) {
	fs := &fakeStore{hasCreatingSpec: false, oldest: nil}
	launchCalled := false
	d := dispatch.New(fs, func(store.Project, store.Prompt) error {
		launchCalled = true
		return nil
	})

	d.TryDispatchNext(context.Background())

	if launchCalled {
		t.Error("launch should not be called when no prompt is NOT_STARTED")
	}
}

func TestTryDispatchNext_SuccessfulLaunch_MarksCreatingSpec(t *testing.T) {
	prompt := &store.Prompt{ID: 42, ProjectName: "demo", TaskName: "task", PromptText: "text", State: store.StateNotStarted}
	fs := &fakeStore{
		hasCreatingSpec: false,
		oldest:          prompt,
		projects:        map[string]store.Project{"demo": {Name: "demo", Path: `C:\demo`}},
	}
	var gotProject store.Project
	var gotPrompt store.Prompt
	d := dispatch.New(fs, func(p store.Project, pr store.Prompt) error {
		gotProject, gotPrompt = p, pr
		return nil
	})

	d.TryDispatchNext(context.Background())

	if gotProject.Name != "demo" || gotPrompt.ID != 42 {
		t.Fatalf("launch called with (%+v, %+v), want project demo / prompt 42", gotProject, gotPrompt)
	}
	if fs.markedID != 42 {
		t.Errorf("MarkCreatingSpec called with id=%d, want 42", fs.markedID)
	}
}

func TestTryDispatchNext_LaunchFails_RecordsErrorAndDoesNotMark(t *testing.T) {
	prompt := &store.Prompt{ID: 7, ProjectName: "demo", TaskName: "task", PromptText: "text", State: store.StateNotStarted}
	fs := &fakeStore{
		hasCreatingSpec: false,
		oldest:          prompt,
		projects:        map[string]store.Project{"demo": {Name: "demo", Path: `C:\demo`}},
	}
	d := dispatch.New(fs, func(store.Project, store.Prompt) error {
		return errors.New("path not found")
	})

	d.TryDispatchNext(context.Background())

	if fs.markedID != 0 {
		t.Errorf("MarkCreatingSpec should not be called on launch failure, got id=%d", fs.markedID)
	}
	if fs.launchErrorID != 7 || fs.launchErrorMsg != "path not found" {
		t.Errorf("SetLaunchError called with (%d, %q), want (7, \"path not found\")", fs.launchErrorID, fs.launchErrorMsg)
	}
}
