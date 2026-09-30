//go:build unix && !linux && !darwin

package proc

import "syscall"

// StartTime reports whether process pid is running. Without a portable start
// time on this platform, start is empty and a reused pid reads as running.
func StartTime(pid int) (start string, ok bool) {
	err := syscall.Kill(pid, 0)
	return "", err == nil || err == syscall.EPERM
}
