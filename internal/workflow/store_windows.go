//go:build windows

package workflow

// Without a reliable liveness probe, refuse stale lock recovery.
func processDead(pid int) bool { return false }
