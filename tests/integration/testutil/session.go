package testutil

import (
	"context"
	"errors"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// OpenSessionStore isolates storage while exercising canonical project resolution.
func OpenSessionStore(t testing.TB, workspace string) *session.Store {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := session.OpenStore(context.Background(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// ImportSession creates native fixture history; later changes use AppendMessages
// or explicit state events, never snapshot replacement or save-time deduplication.
func ImportSession(store *session.Store, options session.CreateOptions, messages []ai.Message) (err error) {
	owner, err := store.Create(context.Background(), options)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, owner.Close()) }()
	return AppendMessages(owner, messages)
}

// AppendMessages supplies actual inference provenance for fixture assistant output.
// Tool result fixtures get an allow gate before their native result message.
func AppendMessages(owner *session.Session, messages []ai.Message) error {
	ctx := context.Background()
	for _, message := range messages {
		if message.ID == "" {
			message.ID = core.NewMessageID()
		}
		if message.Role == ai.RoleAssistant {
			snapshot := owner.Snapshot()
			request := &ai.Request{Messages: snapshot.ActiveMessages()}
			inference, err := owner.StartInference(ctx, session.InferenceOptions{Purpose: session.PurposeTurn, TurnID: core.NewMessageID(), Provider: "fixture", Model: ai.Model{ID: "fixture"}, Request: request})
			if err != nil {
				return err
			}
			if err := inference.Finish(ctx, &ai.Response{Content: message.Content, StopReason: ai.StopEndTurn}, nil, session.Event{Payload: session.MessageAppendPayload{Messages: []ai.Message{message}}}); err != nil {
				return err
			}
			continue
		}
		for _, result := range message.ToolResults() {
			snapshot := owner.Snapshot()
			decided := false
			for _, event := range snapshot.Events() {
				if gate, ok := event.Payload.(*session.PermissionDecidePayload); ok && gate.CallID == result.ToolCallID {
					decided = true
				}
			}
			if !decided {
				if err := owner.Append(ctx, session.Event{Payload: session.PermissionDecidePayload{CallID: result.ToolCallID, Decision: "allow", By: session.ByConfig}}); err != nil {
					return err
				}
			}
		}
		if err := owner.Append(ctx, session.Event{Payload: session.MessageAppendPayload{Messages: []ai.Message{message}}}); err != nil {
			return err
		}
	}
	return nil
}
