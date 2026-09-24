//go:build windows

package proc

import (
	"os/exec"
	"syscall"
	"testing"
	"time"

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
