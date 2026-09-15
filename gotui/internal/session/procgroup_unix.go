//go:build unix

package session

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in its own process group so a cancellation can
// take down whatever it spawned.
//
// TokenWatch is started as `uv run tokenwatch start`, which runs the FastAPI
// proxy with uvicorn inside the same tree. Killing only the direct child would
// leave uvicorn holding the inherited pipes and the Oracle pool, so the reader
// would block until it exited on its own and the UI would look frozen.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup signals the whole group, which closes inherited pipes
// immediately instead of waiting for descendants to exit.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
