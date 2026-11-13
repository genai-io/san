package subagent

import (
	"context"
	"testing"
	"time"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestTypedChildContinuation(t *testing.T) {
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
	d := aitest.New(aitest.Says("first answer"), aitest.Says("continued answer"))
	e := NewExecutor(continuationProvider{d}, cwd, "aitest", nil)
	if err := e.SetTypedSessions(store, parent); err != nil {
		t.Fatal(err)
	}
	a, err := e.RunBackground(tool.AgentExecRequest{Prompt: "original task", ParentToolCallID: "launch"})
	if err != nil {
		t.Fatal(err)
	}
	if !a.WaitForCompletion(5 * time.Second) {
		t.Fatal("hung")
	}
	status := a.GetStatus()
	if status.Error != "" {
		t.Fatalf("first: %+v", status)
	}
	child, err := store.Open(ctx, status.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := child.Snapshot()
	link := snapshot.Identity().Parent
	if link == nil || link.Session != parent.ID() || link.CallID != "launch" {
		t.Fatalf("link: %+v", link)
	}
	before := len(snapshot.Events())
	child.Close()
	b, err := e.RunBackground(tool.AgentExecRequest{ResumeTaskID: a.GetID(), Prompt: "continue task", ParentToolCallID: "continue"})
	if err != nil {
		t.Fatal(err)
	}
	if !b.WaitForCompletion(5 * time.Second) {
		t.Fatal("hung")
	}
	if status := b.GetStatus(); status.Error != "" {
		t.Fatalf("second: %+v", status)
	}
	child, err = store.Open(ctx, status.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	snapshot = child.Snapshot()
	if len(snapshot.Events()) <= before {
		t.Fatal("continuation not persisted")
	}
	if got := d.Last().Messages; len(got) < 3 {
		t.Fatalf("missing native history: %+v", got)
	}
}

func TestTypedChildClosedStoreFailsBeforeDispatch(t *testing.T) {
	ctx := context.Background()
	store, err := session.OpenStore(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.Create(ctx, session.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	d := aitest.New(aitest.Says("must not dispatch"))
	e := NewExecutor(continuationProvider{d}, t.TempDir(), "aitest", nil)
	if err := e.SetTypedSessions(store, parent); err != nil {
		t.Fatal(err)
	}
	store.Close()
	if _, err := e.Run(ctx, tool.AgentExecRequest{Prompt: "work", ParentToolCallID: "launch"}); err == nil {
		t.Fatal("closed storage accepted")
	}
}
