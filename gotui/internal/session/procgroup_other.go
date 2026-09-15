//go:build !unix

package session

import "os/exec"

// setProcessGroup is a no-op where process groups are unavailable.
func setProcessGroup(*exec.Cmd) {}

// killProcessGroup falls back to killing the direct child.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
