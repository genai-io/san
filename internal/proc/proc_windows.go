//go:build windows

package proc

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DetachSession is a no-op on Windows: there is no controlling-terminal /
// /dev/tty concept to detach from. Grandchildren are reached at termination
// instead — see Start and TerminateGroup.
func DetachSession(cmd *exec.Cmd) {}

// GroupLeaderPID reports that Windows offers no signalable process group, so
// callers must fall back to single-process controls.
func GroupLeaderPID(cmd *exec.Cmd) (pid int, ok bool) { return 0, false }

// jobs holds the Job Object each started command's processes run in, keyed by
// its *exec.Cmd, until the child exits or TerminateGroup ends the job.
var jobs sync.Map

// Start starts cmd and puts its child in a new Job Object, which every process
// it spawns joins too, so TerminateGroup reaches a descendant whose own parent
// has already exited. A child that cannot be assigned (breakaway forbidden by
// an outer job) still runs; TerminateGroup then falls back to the snapshot.
func Start(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil
	}
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		_ = windows.CloseHandle(h)
		_ = windows.CloseHandle(job)
		return nil
	}
	jobs.Store(cmd, job)
	go func() {
		_, _ = windows.WaitForSingleObject(h, windows.INFINITE)
		_ = windows.CloseHandle(h)
		if job, ok := jobs.LoadAndDelete(cmd); ok {
			_ = windows.CloseHandle(job.(windows.Handle))
		}
	}()
	return nil
}

// TerminateGroup terminates cmd's child and every process descended from it:
// its Job Object when Start made one, plus the tree read from a process
// snapshot, which also covers a child spawned before the job was assigned;
// sig is ignored. A child that already exited is reported as success, as on
// Unix.
func TerminateGroup(cmd *exec.Cmd, _ syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	descendants := descendantsOf(uint32(cmd.Process.Pid))
	err := cmd.Process.Kill()
	if job, ok := jobs.LoadAndDelete(cmd); ok {
		_ = windows.TerminateJobObject(job.(windows.Handle), 1)
		_ = windows.CloseHandle(job.(windows.Handle))
	}
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
