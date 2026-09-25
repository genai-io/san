package core

import (
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

// The provider's count for the last call is exact; only what followed it is
// estimated. The SDK's whole-prompt estimate runs deliberately high, so leaning
// on it would compact long before the window is actually full.
func TestPromptMeasureAnchorsOnTheProvidersCount(t *testing.T) {
	var m promptMeasure
	sent := []Message{UserMessage("a", nil), AssistantMessage("b", "", nil), UserMessage("c", nil)}
	m.sending(len(sent))
	m.answered(ai.Usage{Input: 1_000, CacheRead: 149_000, Output: 1_000})

	next := append(sent, AssistantMessage("reply", "", nil), UserMessage("tool output", nil))
	got := m.prompt(next, 999_999)
	if got < 151_000 || got > 151_100 {
		t.Fatalf("prompt() = %d, want the 151000 counted plus a few estimated tokens", got)
	}
}

// A replaced conversation no longer matches the anchor, so the measure falls
// back to the estimate instead of reading "full" forever.
func TestPromptMeasureFallsBackOnceReplaced(t *testing.T) {
	var m promptMeasure
	m.sending(3)
	m.answered(ai.Usage{Input: 150_000})

	summary := []Message{UserMessage("summary", nil)}
	if got := m.prompt(summary, 42); got != 42 {
		t.Fatalf("prompt() on a shorter conversation = %d, want the estimate", got)
	}

	m.reset()
	long := make([]Message, 10)
	for i := range long {
		long[i] = UserMessage("x", nil)
	}
	if got := m.prompt(long, 42); got != 42 {
		t.Fatalf("prompt() after reset = %d, want the estimate", got)
	}
}
