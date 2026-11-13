package agent

import (
	"context"
	"iter"
	"reflect"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestRuntimeAcceptedInputDuringInferencePreservesNativeOrder(t *testing.T) {
	for _, mode := range []string{"source", "completion"} {
		t.Run(mode, func(t *testing.T) {
			owner, params, _ := resumeFixture(t)
			message := core.UserMessage("exact prepared queued input", nil)
			message.ID = "queued-input"
			input := PendingInput{Message: message}
			if mode == "source" {
				_, _, source := childInputSource(t, owner, params.CWD)
				input.Source, input.Destination = &source, owner
			} else {
				input.Completion = owner.Completion(session.PurposeAutopilot)
				if _, err := llm.Complete(t.Context(), runOnceProvider{aitest.New(aitest.Says("utility suggestion"))}, llm.CompletionOptions{Admission: input.Completion.Admission()}); err != nil {
					t.Fatal(err)
				}
			}
			var runtime Runtime
			driver := aitest.New(func(ctx context.Context, request *ai.Request) iter.Seq2[ai.Delta, error] {
				if err := runtime.SendInput(input); err != nil {
					t.Errorf("queue input during inference: %v", err)
				}
				return aitest.Says("first answer")(ctx, request)
			}, aitest.Says("second answer"))
			params.Provider = runOnceProvider{driver}
			if _, err := runtime.Resume(params); err != nil {
				t.Fatal(err)
			}
			defer resumeStop(t, &runtime)
			first := core.UserMessage("initial input", nil)
			if err := runtime.Send(first); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			finished := false
			for !finished {
				select {
				case event := <-runtime.Outbox():
					if _, ok := event.(core.TurnEnded); ok && driver.Calls() == 2 {
						finished = true
					}
				case <-ctx.Done():
					t.Fatal("queued input did not complete", ctx.Err())
				}
			}
			resumeStop(t, &runtime)
			projection := owner.Snapshot()
			if !reflect.DeepEqual(runtime.Messages(), projection.ActiveMessages()) || !reflect.DeepEqual(runtime.Messages(), projection.Messages()) {
				t.Fatalf("native order differs from resume history: native=%+v persisted=%+v", runtime.Messages(), projection.ActiveMessages())
			}
			messages := runtime.Messages()
			if len(messages) != 4 || messages[1].Text() != "first answer" || messages[2].ID != message.ID || messages[3].Text() != "second answer" {
				t.Fatalf("wrong consumed conversation: %+v", messages)
			}
			starts := 0
			for _, event := range projection.Events() {
				if start, ok := event.Payload.(*session.InferenceStartPayload); ok && start.Purpose == session.PurposeTurn {
					starts++
					if start.Input.Conversation == nil || start.Input.Standalone != "" {
						t.Fatal("standalone fallback masked accepted-input ordering")
					}
				}
			}
			if starts != 2 {
				t.Fatalf("turn inference count: %d", starts)
			}
		})
	}
}
