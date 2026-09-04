package launcher_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"soulman/projects-svc/launcher"
)

func TestLaunch_MissingProjectPath_ReturnsErrProjectPathNotFound(t *testing.T) {
	project := launcher.Project{Name: "demo", Path: filepath.Join(t.TempDir(), "does-not-exist")}
	prompt := launcher.Prompt{ID: 1, TaskName: "task", PromptText: "do it"}

	err := launcher.Launch(project, prompt, "9017")
	if !errors.Is(err, launcher.ErrProjectPathNotFound) {
		t.Fatalf("Launch error = %v, want ErrProjectPathNotFound", err)
	}
}

func TestLaunch_ProjectPathIsAFile_ReturnsErrProjectPathNotFound(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	project := launcher.Project{Name: "demo", Path: file}
	prompt := launcher.Prompt{ID: 1, TaskName: "task", PromptText: "do it"}

	err := launcher.Launch(project, prompt, "9017")
	if !errors.Is(err, launcher.ErrProjectPathNotFound) {
		t.Fatalf("Launch error = %v, want ErrProjectPathNotFound", err)
	}
}
