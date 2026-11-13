package app

import (
	"context"
	"errors"
	"iter"
	"reflect"
	"testing"

	"github.com/genai-io/san/internal/judge"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestBashPromptRecording(t *testing.T) {
	for _, mode := range []string{"answer", "skip", "invalid", "provider-error", "cancelled", "closed-admission", "closed-finish", "stale-session"} {
		t.Run(mode, func(t *testing.T) {
			m, owner := autopilotRecordingModel(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			driver := aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
				text := `{"action":"answer","input":"y"}`
				var callErr error
				switch mode {
				case "skip":
					text = `{"action":"skip"}`
				case "invalid":
					text = "invalid reply"
				case "provider-error":
					callErr = errors.New("provider failed")
				case "cancelled":
					cancel()
				case "closed-finish":
					if err := owner.Close(); err != nil {
						t.Error(err)
					}
				}
				return aitest.Replies(ai.Response{Content: ai.TextContent(text), Usage: ai.Usage{Input: 17, Output: 4}, Err: callErr})(ctx, req)
			})
			provider := &compactHTTPProvider{client: ai.NewClientWithDriver(driver, ai.Model{ID: "resolved-judge"})}
			m.autopilot.Store(&autopilotRuntime{judge: judge.New(provider, "alias"), sessionID: owner.ID()})
			responder := bashPromptResponder{model: m, recording: owner, sessionID: owner.ID()}
			if mode == "closed-admission" {
				if err := owner.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "stale-session" {
				m.autopilot.Store(&autopilotRuntime{judge: judge.New(provider, "alias"), sessionID: "replacement"})
			}
			input, ok := responder.RequestAnswer(ctx, "go test ./...", "Continue? [Y/n]")
			if ok != (mode == "answer") || ok && input != "y" || !ok && input != "" {
				t.Fatalf("unsafe prompt response: input=%q answered=%t", input, ok)
			}
			if mode == "closed-admission" || mode == "stale-session" {
				if driver.Calls() != 0 {
					t.Fatal("prompt dispatched after rejection")
				}
				return
			}
			if mode == "closed-finish" {
				return
			}
			snapshot := owner.Snapshot()
			events := snapshot.Events()
			if len(events) != 3 || len(snapshot.Messages()) != 0 || len(snapshot.Unfinished().Inferences) != 0 {
				t.Fatalf("invented prompt output or lost accounting: %+v", events)
			}
			start := events[1].Payload.(*session.InferenceStartPayload)
			if start.Purpose != session.PurposeAutopilot || start.Input.Standalone == "" || *start.Request.Model != "resolved-judge" {
				t.Fatalf("wrong prompt inference: %+v", start)
			}
			if mode != "cancelled" {
				rebuilt, err := owner.InferenceRequest(t.Context(), events[1].Seq)
				if err != nil || !reflect.DeepEqual(rebuilt, driver.Last()) {
					t.Fatalf("prompt request did not round trip: %v", err)
				}
				finish := events[2].Payload.(*session.InferenceFinishPayload)
				if finish.Usage.Input != 17 || finish.Usage.Output != 4 {
					t.Fatalf("lost prompt usage: %+v", finish)
				}
			}
		})
	}
}
