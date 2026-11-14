//go:build !windows

package workflow

import (
	"errors"
	"syscall"
)

func processDead(pid int) bool { return pid > 0 && errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }
