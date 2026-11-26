package proc

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// StartTime identifies when process pid started, so a pid the kernel has
// since handed to another process is not mistaken for it. ok is false when no
// such process is running.
func StartTime(pid int) (start string, ok bool) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || info.Proc.P_pid != int32(pid) {
		return "", false
	}
	t := info.Proc.P_starttime
	return fmt.Sprintf("%d.%06d", t.Sec, t.Usec), true
}
