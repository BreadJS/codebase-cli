//go:build windows

package util

import "os/exec"

// SetProcGroup is a no-op on Windows (no Setpgid support).
func SetProcGroup(cmd *exec.Cmd) {}

// KillProcGroup kills the process on Windows.
func KillProcGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}
