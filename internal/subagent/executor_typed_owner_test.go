package subagent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestTypedSessionsRejectWrongStoreHandle(t *testing.T) {
	ctx := context.Background()
	cwd := t.TempDir()
	original, err := session.OpenStore(ctx, cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	parent, err := original.Create(ctx, session.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	// Even another store for the same project and parent ID is not this owner.
	other, err := session.OpenStore(ctx, cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	e := NewExecutor(continuationProvider{aitest.New()}, cwd, "aitest", nil)
	if err := e.SetSessions(other, parent); err == nil {
		t.Fatal("accepted parent from another store handle")
	}
	crossProject, err := session.OpenStore(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer crossProject.Close()
	duplicate, err := crossProject.Create(ctx, session.CreateOptions{ID: parent.ID()})
	if err != nil {
		t.Fatal(err)
	}
	defer duplicate.Close()
	if err := e.SetSessions(crossProject, parent); err == nil {
		t.Fatal("accepted cross-project parent with matching ID")
	}
	if err := e.SetSessions(original, parent); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedTypedChildRetainsOriginalOwners(t *testing.T) {
	ctx := context.Background()
	cwd := t.TempDir()
	original, err := session.OpenStore(ctx, cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	parent, err := original.Create(ctx, session.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	args, _ := json.Marshal(map[string]string{"file_path": parent.Path()})
	d := aitest.New(aitest.Asks(ai.ToolCall{ID: "read", Name: "Read", Input: string(args)}), aitest.Says("done"))
	e := NewExecutor(continuationProvider{d}, cwd, "aitest", nil)
	contexts := 0
	e.toolContext = func(ctx context.Context, _ string) context.Context {
		contexts++
		if got := task.SessionIDFromContext(ctx); got != parent.ID() {
			t.Errorf("worker task context followed replacement: %s", got)
		}
		return ctx
	}
	if err := e.SetSessions(original, parent); err != nil {
		t.Fatal(err)
	}
	run, err := e.prepareRun(ctx, tool.AgentExecRequest{TaskID: "worker", Prompt: "work", ParentToolCallID: "launch"})
	if err != nil {
		t.Fatal(err)
	}
	run.checkpoint = newContinuationState(run, parent.ID(), "worker")
	replacement, err := original.Create(ctx, session.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if err := e.SetSessions(original, replacement); err != nil {
		t.Fatal(err)
	}
	if err := e.openNative(ctx, run); err != nil {
		t.Fatal(err)
	}
	defer run.recording.Close()
	snapshot := run.recording.Snapshot()
	link := snapshot.Identity().Parent
	if run.store != original || run.parent != parent || link == nil || link.Session != parent.ID() || link.CallID != "launch" {
		t.Fatalf("prepared run followed replacement owner: %+v", link)
	}
	if run.checkpoint.parentOwner != parent {
		t.Fatal("checkpoint captured replacement parent")
	}
	if _, err := e.runPrepared(ctx, run); err != nil {
		t.Fatal(err)
	}
	if contexts < 2 {
		t.Fatalf("did not observe gate and tool execution contexts: %d", contexts)
	}
}
