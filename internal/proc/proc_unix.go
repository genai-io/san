//go:build unix

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

// DetachSession configures cmd so the spawned process starts in a new session
// with no controlling terminal. Programs that try to read from /dev/tty — a
// password/confirmation prompt, an editor, ssh — then fail fast with ENXIO
// instead of stealing the parent TUI's terminal and hanging. The session
// leader is also a new process-group leader (pgid == pid), so TerminateGroup
// still reaches the whole group; there is no need to also call SetProcessGroup.
func DetachSession(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}

// TerminateGroup sends sig to the process group led by cmd. The cmd's
// in-memory Process handle is used to derive the PGID rather than a raw PID,
// so this is safe against PID reuse. A missing process (ESRCH) is treated as
// success because the caller's intent — "stop this process" — is already
// satisfied.
func TerminateGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
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
