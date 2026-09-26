//go:build windows

package proc

import (
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Terminating a shell takes the program it started with it: cmd runs ping as
// its child, and ping must not outlive the kill.
func TestTerminateGroupReachesGrandchildren(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "ping -n 60 127.0.0.1 > NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	pid := uint32(cmd.Process.Pid)
	grandchildren := descendantsOf(pid)
	for deadline := time.Now().Add(10 * time.Second); len(grandchildren) == 0 && time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
		grandchildren = descendantsOf(pid)
	}
	if len(grandchildren) == 0 {
		t.Fatal("cmd never started ping")
	}

	if err := TerminateGroup(cmd, syscall.SIGKILL); err != nil {
		t.Fatalf("TerminateGroup: %v", err)
	}
	_ = cmd.Wait()

	for _, pid := range grandchildren {
		h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
		if err != nil {
			continue // already gone
		}
		event, _ := windows.WaitForSingleObject(h, 5000)
		_ = windows.CloseHandle(h)
		if event != windows.WAIT_OBJECT_0 {
			t.Errorf("grandchild %d survived TerminateGroup", pid)
		}
	}
}

// A process whose own parent already exited is gone from the snapshot's tree,
// but not from the job Start put the whole command in.
func TestTerminateGroupReachesAnOrphanedDescendant(t *testing.T) {
	before := pingPIDs()
	cmd := exec.Command("cmd", "/c", "(cmd /c start /b ping -n 61 127.0.0.1 >NUL) & ping -n 60 127.0.0.1 >NUL")
	if err := Start(cmd); err != nil {
		t.Fatal(err)
	}

	// Wait until both pings run and the inner cmd has exited, orphaning one.
	var started []uint32
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		started = started[:0]
		for pid := range pingPIDs() {
			if !before[pid] {
				started = append(started, pid)
			}
		}
		if len(started) == 2 && len(descendantsOf(uint32(cmd.Process.Pid))) == 1 {
			break
		}
	}
	if len(started) != 2 {
		t.Fatalf("started %d pings, want 2", len(started))
	}

	if err := TerminateGroup(cmd, syscall.SIGKILL); err != nil {
		t.Fatalf("TerminateGroup: %v", err)
	}
	_ = cmd.Wait()

	for _, pid := range started {
		h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
		if err != nil {
			continue // already gone
		}
		event, _ := windows.WaitForSingleObject(h, 5000)
		_ = windows.CloseHandle(h)
		if event != windows.WAIT_OBJECT_0 {
			t.Errorf("ping %d survived TerminateGroup", pid)
		}
	}
}

// pingPIDs lists every running ping.exe.
func pingPIDs() map[uint32]bool {
	out := map[uint32]bool{}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return out
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "ping.exe") {
			out[entry.ProcessID] = true
		}
	}
	return out
}
