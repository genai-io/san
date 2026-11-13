package app

import (
	"context"
	"errors"
	"fmt"
	"testing"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
)

func TestACPStoppedCancellationPreservesJoinedFailures(t *testing.T) {
	durability := errors.New("native semantic durability failure")
	tests := []struct {
		name string
		err  error
		pure bool
	}{
		{"nil", nil, true},
		{"direct cancellation", context.Canceled, true},
		{"direct output cancellation", &sdkagent.AdmissionError{Boundary: "output", Err: context.Canceled}, true},
		{"joined durability", errors.Join(context.Canceled, durability), false},
		{"joined output durability", &sdkagent.AdmissionError{Boundary: "output", Err: errors.Join(context.Canceled, durability)}, false},
		{"wrapped cancellation", fmt.Errorf("unexpected boundary: %w", context.Canceled), false},
		{"other admission boundary", &sdkagent.AdmissionError{Boundary: "tool", Err: context.Canceled}, false},
		{"durability only", durability, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := acpStopFailure(test.err)
			if test.pure {
				if got != nil {
					t.Fatalf("pure cancellation returned %v", got)
				}
				return
			}
			if got != test.err {
				t.Fatalf("changed failure evidence: got %v want %v", got, test.err)
			}
			if errors.Is(test.err, durability) && !errors.Is(got, durability) {
				t.Fatal("lost durability failure")
			}
		})
	}
}
