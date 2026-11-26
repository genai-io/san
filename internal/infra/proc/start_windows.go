package proc

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// StartTime identifies when process pid started, so a pid Windows has since
// handed to another process is not mistaken for it. ok is false when no such
// process is running.
func StartTime(pid int) (start string, ok bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil || code != 259 { // STILL_ACTIVE
		return "", false
	}
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) != nil {
		return "", false
	}
	return fmt.Sprintf("%d", created.Nanoseconds()), true
}
