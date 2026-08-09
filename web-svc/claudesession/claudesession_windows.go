//go:build windows

package claudesession

import (
	"os/exec"
	"syscall"
)

// detach sets Windows-specific process creation flags so the spawned
// claude process is not tied to web-svc's own process group and
// survives a web-svc restart. Mirrors perception-svc/sysmonitor's
// existing convention of a *_windows.go file for OS-specific code —
// this repo's services only ever run on Windows, so no non-Windows
// counterpart is needed (see perception-svc/sysmonitor/stats_windows.go).
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
