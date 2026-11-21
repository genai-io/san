package aside

import (
	"context"
	"iter"
	"reflect"
	"strings"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
)

func TestWithSideQuestion(t *testing.T) {
	call := ai.ToolCallBlock(ai.ToolCall{ID: "c1", Name: "Bash"})
	running := []core.Message{core.UserMessage("run tests", nil), {Role: ai.RoleAssistant, Content: ai.Content{call}}}

	got := withSidePrompt(running, "why?")
	if len(got) != 4 || !reflect.DeepEqual(got[:2], running) {
		t.Fatalf("want pending results and question appended after the exact prefix: %+v", got)
	}
	if r := got[2].Content[0].ToolResult; r == nil || r.ToolCallID != "c1" || len(got[2].Content) != 1 {
		t.Fatalf("in-flight call must get a pending result of its own: %+v", got[2].Content)
	}
	// Text beside tool results is dropped by the OpenAI chat driver.
	if got[3].Content.Has(ai.BlockToolResult) || !strings.HasSuffix(got[3].Content.Text(), "why?") {
		t.Fatalf("question must be a message of its own: %+v", got[3].Content)
	}

	idle := []core.Message{core.UserMessage("hi", nil), core.AssistantMessage("hello", "", nil)}
	if got := withSidePrompt(idle, "why?"); len(got) != 3 {
		t.Fatalf("idle chain gets only the question, got %d messages", len(got))
	}

	// Left open, the request in flight reads as the side answer's to carry out.
	replying := []core.Message{core.UserMessage("write an essay", nil)}
	got = withSidePrompt(replying, "why?")
	if len(got) != 3 || got[1].Role != ai.RoleAssistant || got[1].Content.Text() != sideReplying {
		t.Fatalf("reply in flight must get a placeholder before the question: %+v", got)
	}
}

func TestSnapshotRequestPreservesPrefix(t *testing.T) {
	chain := []core.Message{core.UserMessage("work", nil), core.AssistantMessage("done", "", nil)}
	chain[0].ID, chain[1].ID = core.NewMessageID(), core.NewMessageID()
	tools := []core.ToolSchema{{Name: "second"}, {Name: "first"}}
	for _, tc := range []struct {
		name    string
		schemas []core.ToolSchema
	}{{"cache prefix", tools}, {"Flow draft", nil}} {
		t.Run(tc.name, func(t *testing.T) {
			var got *ai.Request
			driver := aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
				got = req
				return aitest.Says("reply")(ctx, req)
			})
			p := testProvider{client: ai.NewClientWithDriver(driver, ai.Model{ID: "m"})}
			snapshot := NewSnapshot(SnapshotOptions{
				Client: llm.NewClient(p, "m", 100), System: "system", Tools: tc.schemas, Messages: chain, Purpose: "test",
			}, "side prompt", "detail")
			resp, err := snapshot.Complete(t.Context())
			if err != nil || resp.Content.Text() != "reply" {
				t.Fatalf("completion = %q, %v", resp.Content.Text(), err)
			}
			if got.System != "system" || !reflect.DeepEqual(got.Tools, tc.schemas) || len(got.Messages) != len(chain)+1 || !reflect.DeepEqual(got.Messages[:len(chain)], chain) {
				t.Fatalf("unexpected dispatched prefix: %+v", got)
			}
			last := got.Messages[len(chain)]
			if last.ID == "" || !strings.Contains(last.Content.Text(), "side prompt") || !strings.Contains(last.Content.Text(), "detail") {
				t.Fatalf("unexpected side suffix: %+v", last)
			}
		})
	}
}

type testProvider struct {
	llm.Provider
	client *ai.Client
}

func (p testProvider) Client(string, map[string]string) (*ai.Client, error) { return p.client, nil }
func (p testProvider) Name() string                                         { return "test" }
