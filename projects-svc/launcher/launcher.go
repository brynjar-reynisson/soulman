// Package launcher starts a detached, remote-control-enabled Claude Code
// session for one prompt, rooted at its project's path. Mirrors
// web-svc/claudesession.Launch's detachment pattern (see that package's
// doc comment for why --bg plus a null-device stdin/stdout/stderr is
// required on Windows), but takes a free-form project.Path directly
// instead of resolving a folder under a curated root, and passes the
// prompt text (with the fixed spec/implement/notify directive appended)
// as Launch's trailing positional argument — see
// docs/superpowers/specs/2026-09-04-projects-tool-design.md's Data Flow
// section for the exact directive text this builds.
package launcher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

var (
	ErrProjectPathNotFound = errors.New("launcher: project path not found")
	ErrLaunchFailed        = errors.New("launcher: launch failed")
)

type Project struct {
	Name string
	Path string
}

type Prompt struct {
	ID         int64
	TaskName   string
	PromptText string
}

// Launch starts `claude --remote-control --bg --name "<project> <task>"
// "<prompt>"` detached, with its working directory set to project.Path.
// notifyPort is substituted into the directive's curl commands so the
// spawned session calls back on the right loopback-only notify listener
// for this environment (dev vs prod use different ports).
func Launch(project Project, prompt Prompt, notifyPort string) error {
	info, err := os.Stat(project.Path)
	if err != nil || !info.IsDir() {
		return ErrProjectPathNotFound
	}

	sessionName := project.Name + " " + prompt.TaskName
	fullPrompt := prompt.PromptText + "\n\n" + directive(prompt.ID, notifyPort)

	cmd := exec.Command("claude", "--remote-control", "--bg", "--name", sessionName, fullPrompt)
	cmd.Dir = project.Path
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%w: %v", ErrLaunchFailed, err)
	}
	cmd.Process.Release()
	return nil
}

func directive(promptID int64, notifyPort string) string {
	notify := func(state string) string {
		return fmt.Sprintf(
			`curl -s -X POST http://127.0.0.1:%s/notify -H "Content-Type: application/json" -d '{"prompt_id": %d, "state": "%s"}'`,
			notifyPort, promptID, state)
	}
	return fmt.Sprintf(
		"Use Superpowers to figure out via questioning what is being requested, and to write the feature spec. "+
			"Once the feature spec has been accepted, run:\n\n%s\n\n"+
			"then proceed to creating an implementation plan and executing it as usual. "+
			"Once that is complete, run:\n\n%s",
		notify("IMPLEMENTING"), notify("DONE"))
}
