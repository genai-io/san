//go:build windows

package proc

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// SetProcessGroup is a no-op on Windows: the syscall package does not expose
// Setpgid, and adding Job Object support is out of scope for this package.
// Callers should not assume grandchild cleanup works on Windows.
func SetProcessGroup(cmd *exec.Cmd) {}

// DetachSession is a no-op on Windows: there is no controlling-terminal /
// /dev/tty concept to detach from, and session creation is out of scope for
// this package.
func DetachSession(cmd *exec.Cmd) {}

// GroupLeaderPID reports that Windows offers no signalable process group —
// the package does not yet use Job Objects — so callers must fall back to
// single-process controls.
func GroupLeaderPID(cmd *exec.Cmd) (pid int, ok bool) { return 0, false }

// TerminateGroup terminates cmd's direct child process. Windows has no
// signal-based group termination, so sig is ignored and we call Process.Kill,
// which maps to TerminateProcess. Because we use the handle the standard
// library has held since Start, this is safe against PID reuse — the kernel
// keeps the original process record alive while the handle is open.
//
// An already-exited process is reported as success: ErrProcessDone (and its
// historical aliases produced by NewSyscallError around TerminateProcess) are
// translated to nil so callers can treat the "child is already gone" case the
// same way the Unix path does.
func TerminateGroup(cmd *exec.Cmd, _ syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return err
	}
	return nil
}
