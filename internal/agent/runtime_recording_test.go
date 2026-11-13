package agent

import (
	"errors"
	"reflect"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
)

func TestRuntimeRecordsLateAcceptedInputAfterRecorderError(t *testing.T) {
	owner, params, _ := resumeFixture(t)
	ag := newControlledAgent(false)
	var recorder *session.Recorder
	runtime := Runtime{build: func(p BuildParams) (core.Agent, *PermissionGate, error) {
		recorder = p.recorder
		return ag, nil, nil
	}}
	if err := runtime.Start(params, nil); err != nil {
		t.Fatal(err)
	}
	defer resumeStop(t, &runtime)
	<-ag.started

	// Native admission succeeds, but this controlled mailbox never announces
	// MessageAdded. Shutdown must reconcile the pending input through its recorder.
	input := core.UserMessage("exact prepared input with final reminder", nil)
	input.ID = "late-accepted-input"
	if err := runtime.Send(input); err != nil {
		t.Fatal(err)
	}
	if got := <-ag.appended; !reflect.DeepEqual(got, input) {
		t.Fatalf("native admission changed input: %+v", got)
	}
	// An uncommitted replacement is a semantic error, not a broken writer.
	recorder.RecordEvent(sdkagent.MessagesReplaced{Messages: []core.Message{core.UserMessage("uncommitted replacement", nil)}})
	latched := recorder.Err()
	if latched == nil || owner.Err() != nil {
		t.Fatalf("expected semantic recorder error with healthy writer: recorder=%v writer=%v", latched, owner.Err())
	}
	resumeStop(t, &runtime)

	projection := owner.Snapshot()
	want := []core.Message{input}
	if !reflect.DeepEqual(runtime.Messages(), want) || !reflect.DeepEqual(projection.ActiveMessages(), want) {
		t.Fatalf("late input diverged: runtime=%+v recorded=%+v", runtime.Messages(), projection.ActiveMessages())
	}
	count := 0
	for _, event := range projection.Events() {
		if payload, ok := event.Payload.(*session.MessageAppendPayload); ok {
			for _, message := range payload.Messages {
				if message.ID == input.ID {
					count++
				}
			}
		}
	}
	if count != 1 {
		t.Fatalf("late input committed %d times", count)
	}
	if !errors.Is(recorder.Err(), latched) {
		t.Fatalf("shutdown cleared recorder error: %v", recorder.Err())
	}
	if err := recorder.InferenceAdmission(t.Context(), sdkagent.InferenceAdmission{}); !errors.Is(err, latched) {
		t.Fatalf("new admission did not retain failure: %v", err)
	}
}
