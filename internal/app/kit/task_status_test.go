package kit

import "testing"

func TestRoutineStopReasonDoesNotHideSubstantiveDiagnostics(t *testing.T) {
	for _, reason := range []string{"context canceled", " Context cancelled ", "Stopped before completion", "stopped by user"} {
		if !IsRoutineStopReason(reason) {
			t.Errorf("routine cancellation not recognized: %q", reason)
		}
	}
	for _, reason := range []string{"", "provider unavailable", "partial finding\nError: context canceled", "context canceled while saving report"} {
		if IsRoutineStopReason(reason) {
			t.Errorf("substantive detail was treated as routine cancellation: %q", reason)
		}
	}
}
