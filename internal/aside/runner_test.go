package aside

import (
	"context"
	"iter"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
)

func TestApplyChangeRecordsOnlyAcceptedResults(t *testing.T) {
	for _, notice := range []string{"Saved note", ""} {
		t.Run("notice="+notice, func(t *testing.T) {
			ctx := t.Context()
			store, err := session.OpenStore(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			owner, err := store.Create(ctx, session.CreateOptions{ID: "change"})
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			p := testProvider{client: aitest.New(aitest.Says("proposal")).Client()}
			got, err := ApplyChange(ctx, p, llm.CompletionOptions{
				Model: "m", Messages: []core.Message{core.UserMessage("task", nil)},
			}, owner.Completion(session.PurposeMemoryMaintain), func(_ context.Context, text string) (string, error) {
				if text != "proposal" {
					t.Fatalf("unexpected proposal: %q", text)
				}
				return notice, nil
			})
			if err != nil || got != notice {
				t.Fatalf("notice = %q, error = %v", got, err)
			}
			projection := owner.Snapshot()
			var start uint64
			var output, recorded string
			for _, event := range projection.Events() {
				switch payload := event.Payload.(type) {
				case *session.InferenceStartPayload:
					start = event.Seq
				case *session.InferenceFinishPayload:
					if payload.Output != nil {
						output = payload.Output.Text
					}
				case *session.AnnotationAddPayload:
					if event.Source == nil || event.Source.Seq != start {
						t.Fatal("accepted change has no source link")
					}
					recorded = payload.Text
				case *session.MessageAppendPayload:
					t.Fatal("standalone proposal entered the conversation")
				}
			}
			if output != "proposal" || recorded != notice {
				t.Fatalf("output = %q, accepted result = %q", output, recorded)
			}
		})
	}
}

func TestStandaloneAsideRecording(t *testing.T) {
	ctx := t.Context()
	store, err := session.OpenStore(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner, err := store.Create(ctx, session.CreateOptions{ID: "aside"})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	recording := owner.Completion(session.PurposeAutopilot)
	driver := aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		if len(req.Tools) != 0 {
			t.Errorf("standalone call advertised tools: %+v", req.Tools)
		}
		projection := owner.Snapshot()
		if len(projection.Unfinished().Inferences) != 1 {
			t.Error("call dispatched without a recorded admission")
		}
		return aitest.Says("decision")(ctx, req)
	})
	p := testProvider{client: ai.NewClientWithDriver(driver, ai.Model{ID: "m"})}
	resp, err := Complete(ctx, p, llm.CompletionOptions{
		Model: "m", Messages: []core.Message{core.UserMessage("task", nil)}, Tools: []core.ToolSchema{{Name: "unused"}},
	}, recording)
	if err != nil || resp.Content.Text() != "decision" {
		t.Fatalf("completion = %q, %v", resp.Content.Text(), err)
	}
	projection := owner.Snapshot()
	if len(projection.Unfinished().Inferences) != 1 {
		t.Fatal("runner finalized inference before its owner could accept it")
	}
	if err := recording.Finish(ctx); err != nil {
		t.Fatal(err)
	}
	projection = owner.Snapshot()
	for _, event := range projection.Events() {
		if payload, ok := event.Payload.(*session.InferenceFinishPayload); ok {
			if payload.Output == nil || payload.Output.Text != "decision" {
				t.Fatalf("historical output lost: %+v", payload.Output)
			}
			return
		}
	}
	t.Fatal("missing inference finish")
}
