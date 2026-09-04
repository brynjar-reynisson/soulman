//go:build windows

package launcher

import (
	"os/exec"
	"syscall"
)

// detach ensures the spawned claude process is not tied to projects-svc's
// own process group, so it survives a projects-svc restart — identical to
// web-svc/claudesession_windows.go's detach.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
