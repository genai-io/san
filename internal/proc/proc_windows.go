//go:build windows

package proc

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DetachSession is a no-op on Windows: there is no controlling-terminal /
// /dev/tty concept to detach from. Grandchildren are reached at termination
// instead — see TerminateGroup.
func DetachSession(cmd *exec.Cmd) {}

// GroupLeaderPID reports that Windows offers no signalable process group, so
// callers must fall back to single-process controls.
func GroupLeaderPID(cmd *exec.Cmd) (pid int, ok bool) { return 0, false }

// TerminateGroup terminates cmd's child and every process descended from it,
// read from a process snapshot; sig is ignored. A child that already exited
// is reported as success, as on Unix.
//
// ponytail: a descendant whose parent already exited is missed, as a setsid'd
// daemon is on Unix. A Job Object would catch it, but needs a post-Start call
// at every call site: nothing can be assigned before the process exists.
func TerminateGroup(cmd *exec.Cmd, _ syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	descendants := descendantsOf(uint32(cmd.Process.Pid))
	err := cmd.Process.Kill()
	for _, pid := range descendants {
		if h, openErr := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid); openErr == nil {
			_ = windows.TerminateProcess(h, 1)
			_ = windows.CloseHandle(h)
		}
	}
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

// descendantsOf lists every process below root, read from one snapshot.
func descendantsOf(root uint32) []uint32 {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snapshot)

	children := make(map[uint32][]uint32)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		children[entry.ParentProcessID] = append(children[entry.ParentProcessID], entry.ProcessID)
	}

	// Breadth-first, with the result as the queue. seen guards against a
	// reused PID making the parent links loop.
	out := []uint32{root}
	seen := map[uint32]bool{root: true}
	for i := 0; i < len(out); i++ {
		for _, child := range children[out[i]] {
			if !seen[child] {
				seen[child] = true
				out = append(out, child)
			}
		}
	}
	return out[1:]
}
