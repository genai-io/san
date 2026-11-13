package subagent

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestTypedChildSourceEvidence(t *testing.T) {
	ctx := context.Background()
	cwd := t.TempDir()
	store, err := session.OpenStore(ctx, cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	parent, err := store.Create(ctx, session.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	d := aitest.New(aitest.Says("accepted answer"))
	e := NewExecutor(continuationProvider{d}, cwd, "aitest", nil)
	if err := e.SetSessions(store, parent); err != nil {
		t.Fatal(err)
	}
	result, err := e.Run(ctx, tool.AgentExecRequest{Prompt: "work", ParentToolCallID: "launch"})
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceMessageSeq == 0 || result.Identity.SessionID == "" {
		t.Fatalf("missing source: %+v", result)
	}
	child, err := store.Open(ctx, result.Identity.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	snapshot := child.Snapshot()
	events := snapshot.Events()
	if events[0].Type != session.EventSessionCreate {
		t.Fatal("child not created first")
	}
	found := false
	for _, event := range events {
		if event.Seq == result.SourceMessageSeq {
			payload, ok := event.Payload.(*session.MessageAppendPayload)
			if !ok || payload.Messages[0].Text() != result.Content {
				t.Fatalf("invalid source: %+v", event)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("source not committed")
	}
}
