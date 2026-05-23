//go:build !windows

package proc

import (
	"errors"
	"os/exec"
	"syscall"
)

// SetProcessGroup configures cmd so the spawned process becomes the leader of
// a new process group, allowing TerminateGroup to deliver a signal to the
// whole group.
func SetProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// TerminateGroup sends sig to the process group led by cmd. A missing process
// (ESRCH) is treated as success because the caller's intent — "stop this
// process" — is already satisfied.
func TerminateGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return killGroup(cmd.Process.Pid, sig)
}

// TerminateGroupByPID is like TerminateGroup but takes a raw PID, for callers
// that hold only the PID (e.g. background task records).
func TerminateGroupByPID(pid int, sig syscall.Signal) error {
	return killGroup(pid, sig)
}

func killGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, sig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	return nil
}
