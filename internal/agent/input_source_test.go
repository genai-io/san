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

func childInputSource(t *testing.T, parent *session.Session, cwd string) (*session.Store, *session.Session, session.EventRef) {
	t.Helper()
	store, err := session.OpenStore(t.Context(), cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	child, err := store.Create(t.Context(), session.CreateOptions{Parent: &session.ParentLink{Session: parent.ID(), CallID: "launch"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Close() })
	completion := child.Completion(session.PurposeAutopilot)
	driver := aitest.New(aitest.Says("child finding"))
	if _, err := llm.Complete(t.Context(), runOnceProvider{driver}, llm.CompletionOptions{Admission: completion.Admission()}); err != nil {
		t.Fatal(err)
	}
	if err := completion.FinishMessage(t.Context(), ai.Message{ID: "child-output", Role: ai.RoleAssistant, Content: ai.TextContent("child finding")}); err != nil {
		t.Fatal(err)
	}
	projection := child.Snapshot()
	for _, event := range projection.Events() {
		if _, ok := event.Payload.(*session.MessageAppendPayload); ok {
			return store, child, session.EventRef{Session: child.ID(), Seq: event.Seq}
		}
	}
	t.Fatal("child result has no committed source")
	return nil, nil, session.EventRef{}
}

func TestChildInputCommitsPreparedSourceBeforeNativeAcceptance(t *testing.T) {
	owner, params, driver := resumeFixture(t)
	store, child, source := childInputSource(t, owner, params.CWD)
	input := PendingInput{Message: core.UserMessage("child finding", nil), Source: &source, Destination: owner}
	input.ID = "delivered-child-input"
	prepared := input.Message
	prepared.Content = ai.TextContent("<task-notification>child finding</task-notification>\nfinal reminder")
	observed := false
	params.OnEvent = func(event core.Event) {
		if added, ok := event.(sdkagent.MessageAdded); ok && added.Message.ID == input.ID {
			projection := owner.Snapshot()
			found := false
			for _, event := range projection.Events() {
				if payload, ok := event.Payload.(*session.MessageAppendPayload); ok && payload.Accepted && payload.Messages[0].ID == input.ID {
					found = reflect.DeepEqual(event.Source, &source) && reflect.DeepEqual(payload.Messages[0], prepared)
				}
			}
			if !found {
				t.Error("native input published before exact prepared message and source committed")
			}
			observed = true
		}
	}
	var runtime Runtime
	if _, err := runtime.Resume(params); err != nil {
		t.Fatal(err)
	}
	defer stopRuntime(t, &runtime)
	var queue InputQueue
	queue.Enqueue(input)
	if err := queue.Deliver(runtime.SendInput, func(core.Message) core.Message { return prepared }, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		select {
		case event := <-runtime.Outbox():
			if _, ok := event.(core.TurnEnded); !ok {
				continue
			}
			stopRuntime(t, &runtime)
			if !observed || driver.Calls() != 1 || queue.Len() != 0 {
				t.Fatal("child delivery did not reach native runtime once")
			}
			projection := owner.Snapshot()
			count := 0
			for _, event := range projection.Events() {
				if payload, ok := event.Payload.(*session.MessageAppendPayload); ok && payload.Accepted && payload.Messages[0].ID == input.ID {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("child input recorded %d times", count)
			}
			if err := child.Close(); err != nil {
				t.Fatal(err)
			}
			if err := store.Delete(t.Context(), child.ID()); err != nil {
				t.Fatal(err)
			}
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(t.Context(), owner.ID())
			if err != nil {
				t.Fatalf("replay lost accepted foreign evidence: %v", err)
			}
			defer reopened.Close()
			after := reopened.Snapshot()
			if !reflect.DeepEqual(after.Events(), projection.Events()) {
				t.Fatal("replay changed accepted source after child removal")
			}
			return
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func TestChildInputRejectionRetiresWithoutReplay(t *testing.T) {
	for _, mode := range []string{"inactive", "reopened-owner", "missing-source", "missing-destination", "unknown-source", "closed", "recall-error"} {
		t.Run(mode, func(t *testing.T) {
			owner, params, driver := resumeFixture(t)
			store, _, source := childInputSource(t, owner, params.CWD)
			input := PendingInput{Message: core.UserMessage("child finding", nil), Source: &source, Destination: owner}
			input.ID = "rejected-child-input"
			if mode == "reopened-owner" {
				if err := owner.Close(); err != nil {
					t.Fatal(err)
				}
				replacement, err := store.Open(t.Context(), owner.ID())
				if err != nil {
					t.Fatal(err)
				}
				defer replacement.Close()
				params.Recording = replacement
			}
			var runtime Runtime
			if mode != "inactive" {
				if _, err := runtime.Resume(params); err != nil {
					t.Fatal(err)
				}
				defer stopRuntime(t, &runtime)
			}
			switch mode {
			case "missing-source":
				input.Source = nil
			case "missing-destination":
				input.Destination = nil
			case "unknown-source":
				source.Seq = 99999
			case "closed":
				if err := owner.Close(); err != nil {
					t.Fatal(err)
				}
			}
			var recall func(string) error
			if mode == "recall-error" {
				input.FromUser = true
				recall = func(string) error { return errors.New("recall unavailable") }
			}
			var queue InputQueue
			queue.Enqueue(input)
			if err := queue.Deliver(runtime.SendInput, func(m core.Message) core.Message { return m }, recall); err == nil {
				t.Fatal("invalid child delivery accepted")
			}
			stopRuntime(t, &runtime)
			projection := params.Recording.Snapshot()
			if queue.Len() != 0 || driver.Calls() != 0 || len(projection.Messages()) != 0 {
				t.Fatal("rejected child input remained eligible for replay")
			}
		})
	}
}
