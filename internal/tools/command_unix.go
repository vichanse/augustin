//go:build unix

package tools

import (
	"os/exec"
	"syscall"
)

// Isolate runs the command in its own process group, so cancellation and the
// timeout kill its children too. The returned cleanup kills stragglers left
// running after a normal exit; Augustin supports no background processes.
func Isolate(cmd *exec.Cmd) func() {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	return func() {
		if cmd.Process != nil {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) // the group may already be gone
		}
	}
}
