//go:build windows

package proc

import (
	"os"
	"os/exec"
	"syscall"
)

// SetProcessGroup is a no-op on Windows: the syscall package does not expose
// Setpgid, and the standard library's exec package handles direct-child
// cleanup adequately for the callers in this codebase.
func SetProcessGroup(cmd *exec.Cmd) {}

// TerminateGroup terminates cmd's direct child process. Windows has no
// signal-based group termination, so the sig argument is ignored and we fall
// through to Process.Kill, which maps to TerminateProcess.
func TerminateGroup(cmd *exec.Cmd, _ syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// TerminateGroupByPID resolves pid to a Process and kills it. Returns nil if
// the process can no longer be found, matching the Unix behaviour of treating
// "already gone" as success.
func TerminateGroupByPID(pid int, _ syscall.Signal) error {
	if pid <= 0 {
		return nil
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	return p.Kill()
}
