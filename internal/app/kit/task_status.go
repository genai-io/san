package kit

import "strings"

// IsRoutineStopReason recognizes cancellation plumbing that adds no information
// beside a stopped status. Callers must first establish a stopped outcome: a
// real failure or substantive partial output must not be discarded by this test.
func IsRoutineStopReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "context canceled", "context cancelled", "stopped before completion", "task stopped", "user stopped", "stopped by user":
		return true
	default:
		return false
	}
}
