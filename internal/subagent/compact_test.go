package subagent

import (
	"context"
	"strings"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
)

type summaryProvider struct{}

func (summaryProvider) Client(string, map[string]string) (*ai.Client, error) {
	return aitest.Always(aitest.Says("did steps 1-3")).Client(), nil
}
func (summaryProvider) ListModels(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (summaryProvider) Name() string                                        { return "summary" }

// A compacted subagent keeps its task word for word, not as the summarizer's
// paraphrase — and exactly once, however many times it is compacted.
func TestSubagentCompactionKeepsTaskVerbatim(t *testing.T) {
	const task = "Audit internal/llm for unchecked errors; report file:line only."
	compact := subagentCompactFunc(llm.NewClient(summaryProvider{}, "m", 0), task)

	msgs := []core.Message{core.UserMessage(task, nil), core.UserMessage("progress", nil)}
	for round := 1; round <= 2; round++ {
		summary, err := compact(context.Background(), msgs)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if !strings.HasPrefix(summary, "did steps 1-3") || strings.Count(summary, task) != 1 {
			t.Fatalf("round %d: summary lost or duplicated the task:\n%s", round, summary)
		}
		msgs = []core.Message{core.UserMessage(core.FormatCompactSummary(summary), nil), core.UserMessage("more", nil)}
	}
}
