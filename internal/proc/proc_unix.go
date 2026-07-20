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

// TerminateGroup sends sig to the process group led by cmd. A missing process
// (ESRCH) is treated as success because the caller's intent — "stop this
// process" — is already satisfied.
//
// The caller must guarantee the child has not been reaped. This signals the
// raw PGID, so it does NOT protect against PID reuse on its own: cmd.Process
// .Pid is a plain int and syscall.Kill bypasses os.Process.Signal, which is
// where the done flag set by Wait is checked. Once the child is reaped the
// kernel may reissue that PGID, and the signal lands on whatever holds it now.
//
// Setting it as cmd.Cancel is safe by construction — os/exec invokes Cancel
// before reaping. Calling it directly is best-effort: the caller must skip it
// once the child is reaped, which narrows the reuse window but cannot close it
// (the reap and the check cannot be made atomic). See BashTask.Stop and
// markReaped in internal/task.
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
