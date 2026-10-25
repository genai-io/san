package conv

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
)

func TestWorkerMetadataSurvivesRecentHistory(t *testing.T) {
	activity := retainAgentActivity(nil, "Model: actual-worker-model")
	for i := 0; i < 30; i++ {
		activity = retainAgentActivity(activity, fmt.Sprintf("Read file%d", i))
	}
	if !slices.Contains(activity, "Model: actual-worker-model") {
		t.Fatalf("worker model not retained: %#v", activity)
	}
	if len(activity) != maxAgentActivityHistory+1 {
		t.Fatalf("recent history not bounded: %#v", activity)
	}
}

func TestWorkerHeadersBeforeExpandedDetails(t *testing.T) {
	var calls []core.ToolCall
	for i := 0; i < 4; i++ {
		calls = append(calls, core.ToolCall{ID: fmt.Sprint(i), Name: "Agent", Input: fmt.Sprintf(`{"description":"worker%d","prompt":"long details"}`, i)})
	}
	for _, expanded := range []bool{false, true} {
		p := RenderContext{
			Messages:     []core.ChatMessage{{Role: core.ChatAssistant, ToolCalls: calls, ToolCallsExpanded: expanded}},
			PendingCalls: calls, Width: 80,
		}
		view, active := RenderActiveWorkers(p, 4)
		if !active || strings.Count(view, "\n") != 3 {
			t.Fatalf("wrong worker budget: %q", view)
		}
		for i := 0; i < 4; i++ {
			if !strings.Contains(view, fmt.Sprintf("worker%d", i)) {
				t.Fatalf("missing worker%d: %s", i, view)
			}
		}
		view, _ = RenderActiveWorkers(p, 2)
		if !strings.Contains(view, "3 active workers omitted") {
			t.Fatalf("missing overflow: %s", view)
		}
	}
}
