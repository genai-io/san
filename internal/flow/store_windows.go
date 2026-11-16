//go:build windows

package flow

// Without a reliable liveness probe, refuse stale lock recovery.
func processDead(pid int) bool { return false }
