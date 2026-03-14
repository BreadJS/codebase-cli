//go:build !windows

package util

import (
	"os/exec"
	"syscall"
)

// SetProcGroup sets up process group isolation so child processes
// can be killed together on timeout.
func SetProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// KillProcGroup kills an entire process group by PID.
func KillProcGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
