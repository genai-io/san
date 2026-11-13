package subagent

import (
	"context"
	"strings"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
)

type summaryProvider struct{}

func (summaryProvider) Client(string, map[string]string) (*ai.Client, error) {
	return aitest.Always(aitest.Says("did steps 1-3")).Client(), nil
}
func (summaryProvider) ListModels(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (summaryProvider) Name() string                                        { return "summary" }

func TestSubagentCompactionRetainsChildWriterAndOriginalTask(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := session.OpenStore(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	parent, err := store.Create(t.Context(), session.CreateOptions{ID: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	child, err := store.Create(t.Context(), session.CreateOptions{ID: "child"})
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	original := child
	const task = "Preserve the original task exactly."
	messages := []core.Message{core.UserMessage(task, nil), core.UserMessage("progress", nil), core.UserMessage("more progress", nil)}
	for i := range messages {
		messages[i].ID = core.NewMessageID()
	}
	if err := child.Append(t.Context(), session.Event{Payload: session.MessageAppendPayload{Messages: messages}}); err != nil {
		t.Fatal(err)
	}
	compact := subagentCompactFunc(llm.NewClient(summaryProvider{}, "m", 0), task, child)
	result, err := compact(t.Context(), messages)
	if err != nil {
		t.Fatal(err)
	}
	child = parent // Reconfiguration must not redirect the prepared result.
	message := core.UserMessage(core.FormatCompactSummary(result.Summary), nil)
	message.ID = core.NewMessageID()
	if err := result.Finish(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	if err := result.Close(); err != nil {
		t.Fatal(err)
	}
	p := original.Snapshot()
	active := p.ActiveMessages()
	if len(active) != 1 || active[0].ID != message.ID || strings.Count(active[0].Text(), task) != 1 {
		t.Fatal("child summary lost task, message identity or final formatting")
	}
	p = child.Snapshot()
	if len(p.Events()) != 1 {
		t.Fatal("compaction wrote into parent/replacement session")
	}
}

// A compacted subagent keeps its task word for word, not as the summarizer's
// paraphrase — and exactly once, however many times it is compacted.
func TestSubagentCompactionKeepsTaskVerbatim(t *testing.T) {
	const task = "Audit internal/llm for unchecked errors; report file:line only."
	compact := subagentCompactFunc(llm.NewClient(summaryProvider{}, "m", 0), task, nil)

	msgs := []core.Message{core.UserMessage(task, nil), core.UserMessage("progress", nil)}
	for round := 1; round <= 2; round++ {
		result, err := compact(context.Background(), msgs)
		summary := result.Summary
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if !strings.HasPrefix(summary, "did steps 1-3") || strings.Count(summary, task) != 1 {
			t.Fatalf("round %d: summary lost or duplicated the task:\n%s", round, summary)
		}
		msgs = []core.Message{core.UserMessage(core.FormatCompactSummary(summary), nil), core.UserMessage("more", nil)}
	}
}
