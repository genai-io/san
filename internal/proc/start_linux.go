package proc

import (
	"os"
	"strconv"
	"strings"
)

// StartTime identifies when process pid started, so a pid the kernel has
// since handed to another process is not mistaken for it. ok is false when no
// such process is running.
func StartTime(pid int) (start string, ok bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", false
	}
	// Field 22 is the start time; the command name (field 2) may hold spaces,
	// so count from the closing parenthesis that ends it.
	rest := string(data[strings.LastIndexByte(string(data), ')')+1:])
	fields := strings.Fields(rest)
	if len(fields) < 20 {
		return "", false
	}
	return fields[19], true
}
