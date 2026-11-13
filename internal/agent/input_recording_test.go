package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestRecordedInputRuntimeAcceptance(t *testing.T) {
	for _, mode := range []string{"accepted", "inactive", "queued-inactive", "recall-error", "closed", "wrong-owner"} {
		t.Run(mode, func(t *testing.T) {
			store, err := session.OpenStore(t.Context(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			owner, err := store.Create(t.Context(), session.CreateOptions{ID: "owner"})
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			scope := owner.Completion(session.PurposeAutopilot)
			control := aitest.New(aitest.Replies(ai.Response{Content: ai.TextContent("continue"), Usage: ai.Usage{Input: 17, Output: 4}}))
			if _, err := llm.Complete(t.Context(), runOnceProvider{control}, llm.CompletionOptions{SystemPrompt: "control", Admission: scope.Admission()}); err != nil {
				t.Fatal(err)
			}
			input := PendingInput{Message: core.UserMessage("continue", nil), FromUser: true, Completion: scope}
			input.ID = "prepared-input"
			prepared := input.Message
			prepared.Content = ai.TextContent("continue\n\nfinal reminder")
			runtimeOwner := owner
			if mode == "wrong-owner" {
				runtimeOwner, err = store.Create(t.Context(), session.CreateOptions{ID: "replacement"})
				if err != nil {
					t.Fatal(err)
				}
				defer runtimeOwner.Close()
			}
			recorder := session.NewRecorder(runtimeOwner, "main")
			main := aitest.New(aitest.Says("done"))
			accepted := make(chan core.Message, 1)
			finished := make(chan struct{}, 1)
			runtime := &Runtime{build: func(p BuildParams) (core.Agent, *PermissionGate, error) {
				return core.NewAgent(core.Config{
					System: core.NewSystem(), Tools: core.NewTools(), OutboxBuf: -1,
					Client:             func([]core.Message) (*ai.Client, error) { return main.Client(), nil },
					InferenceAdmission: recorder.InferenceAdmission, ToolAdmission: recorder.ToolAdmission,
					OutputAdmission: recorder.OutputAdmission, OnEvent: p.OnEvent,
				}), NewPermissionGate(nil), nil
			}}
			recall := func(string) error { return nil }
			if mode == "queued-inactive" || mode == "recall-error" {
				var queue InputQueue
				queue.Enqueue(input)
				wantErr := ErrRuntimeInactive
				if mode == "recall-error" {
					wantErr = errors.New("previous recall could not be recorded")
					recall = func(string) error { return &llm.RecordingError{Err: wantErr} }
				}
				err := queue.Deliver(runtime.SendInput, func(m core.Message) core.Message { return m }, recall)
				snapshot := owner.Snapshot()
				if !errors.Is(err, wantErr) || queue.Len() != 0 || len(snapshot.Unfinished().Inferences) != 0 || len(snapshot.Messages()) != 0 {
					t.Fatal("rejected autopilot instruction remained eligible for replay")
				}
				return
			}
			if mode == "inactive" {
				if err := runtime.SendInput(input); !errors.Is(err, ErrRuntimeInactive) {
					t.Fatalf("inactive admission: %v", err)
				}
				snapshot := owner.Snapshot()
				if len(snapshot.Unfinished().Inferences) != 1 || len(snapshot.Messages()) != 0 {
					t.Fatal("runtime rejection consumed caller ownership")
				}
			}
			if err := runtime.Start(BuildParams{Recording: runtimeOwner, OnEvent: func(event core.Event) {
				recorder.RecordEvent(event)
				switch e := event.(type) {
				case sdkagent.MessageAdded:
					if e.Message.Role == ai.RoleUser {
						snapshot := owner.Snapshot()
						if len(snapshot.Unfinished().Inferences) != 0 || !reflect.DeepEqual(snapshot.ActiveMessages(), []ai.Message{e.Message}) {
							t.Error("native input announced before its durable commit")
						}
						accepted <- e.Message
					}
				case core.TurnEnded:
					finished <- struct{}{}
				}
			}}, nil); err != nil {
				t.Fatal(err)
			}
			defer stopRuntime(t, runtime)
			if mode == "closed" {
				if err := owner.Close(); err != nil {
					t.Fatal(err)
				}
			}
			var queue InputQueue
			queue.Enqueue(input)
			err = queue.Deliver(runtime.SendInput, func(m core.Message) core.Message { return prepared }, recall)
			if mode == "closed" || mode == "wrong-owner" {
				var recording *llm.RecordingError
				if !errors.As(err, &recording) || queue.Len() != 0 || len(runtime.Messages()) != 0 || main.Calls() != 0 {
					t.Fatalf("recording failed open: err=%v pending=%d messages=%v calls=%d", err, queue.Len(), runtime.Messages(), main.Calls())
				}
				if mode == "wrong-owner" {
					snapshot := owner.Snapshot()
					if len(snapshot.Unfinished().Inferences) != 0 || len(snapshot.Messages()) != 0 {
						t.Fatal("rejected result was not discarded against its original owner")
					}
				}
				return
			}
			if err != nil || queue.Len() != 0 {
				t.Fatalf("input delivery: %v", err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			select {
			case message := <-accepted:
				if !reflect.DeepEqual(message, prepared) {
					t.Fatalf("accepted a different native message: %+v", message)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			select {
			case <-finished:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err := recorder.Err(); err != nil {
				t.Fatal(err)
			}
			snapshot := owner.Snapshot()
			var inputs int
			for _, event := range snapshot.Events() {
				if payload, ok := event.Payload.(*session.MessageAppendPayload); ok && payload.Messages[0].ID == input.ID {
					inputs++
					if event.Source == nil || event.Source.Seq != 2 {
						t.Fatalf("input lost its autopilot source: %+v", event.Source)
					}
				}
			}
			if inputs != 1 {
				t.Fatalf("input confirmations duplicated recording: %d", inputs)
			}
		})
	}
}
