package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestNativeCancelledOutputAdmissionFinalization(t *testing.T) {
	for _, failedRecording := range []bool{false, true} {
		t.Run(map[bool]string{false: "healthy-writer", true: "latched-recording-failure"}[failedRecording], func(t *testing.T) {
			owner, params, _ := resumeFixture(t)
			params.Provider = runOnceProvider{aitest.New(aitest.Says("must not publish"))}
			entered := make(chan struct{})
			writeErr := errors.New("native output durability failed")
			runtime := Runtime{build: func(p BuildParams) (core.Agent, *PermissionGate, error) {
				client := llm.NewClient(p.Provider, p.ModelID, 0)
				return core.NewAgent(core.Config{Client: client.TurnClient, System: core.NewSystem(), Tools: core.NewTools(), OnEvent: p.OnEvent,
					InferenceAdmission: p.recorder.InferenceAdmission, ToolAdmission: p.recorder.ToolAdmission,
					OutputAdmission: func(ctx context.Context, output sdkagent.OutputAdmission) error {
						close(entered)
						<-ctx.Done()
						if failedRecording {
							_ = p.recorder.Fail(writeErr)
						}
						return p.recorder.OutputAdmission(ctx, output)
					}}), nil, nil
			}}
			if err := runtime.Start(params, nil); err != nil {
				t.Fatal(err)
			}
			defer resumeStop(t, &runtime)
			terminals := make(chan core.Result, 1)
			go func() {
				for event := range runtime.Outbox() {
					if end, ok := event.(core.TurnEnded); ok {
						terminals <- end.Result
					}
				}
			}()
			if err := runtime.Send(core.UserMessage("go", nil)); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			receipt := runtime.Cancel()
			if receipt == nil {
				t.Fatal("missing cancellation receipt")
			}
			if failedRecording {
				if err := receipt.Wait(ctx); !errors.Is(err, writeErr) {
					t.Fatalf("receipt hid native recording failure: %v", err)
				}
				select {
				case <-terminals:
					t.Fatal("recording failure normalized as successful cancellation")
				default:
				}
			} else {
				var terminal core.Result
				select {
				case terminal = <-terminals:
				case <-ctx.Done():
					t.Fatal("missing genuine cancelled terminal")
				}
				if terminal.StopReason != core.StopCanceled || terminal.Finalization != receipt {
					t.Fatalf("wrong cancellation terminal: %+v", terminal)
				}
				short, stop := context.WithTimeout(ctx, 10*time.Millisecond)
				if err := receipt.Wait(short); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("finalized before real consumer cleanup: %v", err)
				}
				stop()
				terminal.Finalization.Acknowledge(nil)
				if err := receipt.Wait(ctx); err != nil {
					t.Fatalf("durably cancelled inference failed finalization: %v", err)
				}
			}
			resumeStop(t, &runtime)
			projection := owner.Snapshot()
			if owner.Err() != nil || len(projection.Unfinished().Inferences) != 0 {
				t.Fatal("writer unhealthy or cancelled inference left unfinished")
			}
			for _, message := range projection.Messages() {
				if message.Role == ai.RoleAssistant {
					t.Fatal("cancelled output published")
				}
			}
			finishes := 0
			for _, event := range projection.Events() {
				if finish, ok := event.Payload.(*session.InferenceFinishPayload); ok {
					finishes++
					if !failedRecording && finish.Outcome != session.OutcomeCancelled {
						t.Fatalf("wrong durable inference outcome: %+v", finish)
					}
				}
			}
			if finishes != 1 {
				t.Fatalf("cancelled inference finished %d times", finishes)
			}
		})
	}
}
