package log

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	queueLogFile *os.File
	queueLogOnce sync.Once
)

// QueueLog writes a timestamped debug line to ./san_debug.log in the working directory.
// Always active (no env var needed). Safe for concurrent use.
func QueueLog(format string, args ...any) {
	if !debugEnabled() {
		return
	}
	queueLogOnce.Do(func() {
		f, err := os.OpenFile("san_debug.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return
		}
		queueLogFile = f
	})
	if queueLogFile == nil {
		return
	}
	ts := time.Now().Format("15:04:05.000")
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(queueLogFile, "%s  %s\n", ts, msg)
}
