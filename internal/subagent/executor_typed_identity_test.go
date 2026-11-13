package subagent

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestTypedContinuationRejectsReplacementParentHandle(t *testing.T) {
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
	d := aitest.New(aitest.Says("first"), aitest.Says("must not run"))
	e := NewExecutor(continuationProvider{d}, cwd, "aitest", nil)
	if err := e.SetTypedSessions(store, parent); err != nil {
		t.Fatal(err)
	}
	a, err := e.RunBackground(tool.AgentExecRequest{Prompt: "work", ParentToolCallID: "launch"})
	if err != nil {
		t.Fatal(err)
	}
	if !a.WaitForCompletion(5 * time.Second) {
		t.Fatal("hung")
	}
	id := parent.ID()
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := store.Open(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if err := e.SetTypedSessions(store, replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RunBackground(tool.AgentExecRequest{ResumeTaskID: a.GetID(), Prompt: "continue", ParentToolCallID: "later"}); err == nil {
		t.Fatal("replacement parent handle accepted")
	}
	if d.Calls() != 1 {
		t.Fatalf("replacement dispatched %d calls", d.Calls())
	}
}

func TestTypedFallbackConfigurationPrecedesDispatch(t *testing.T) {
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
	var run *preparedRun
	checked := false
	fallback := func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		snapshot := run.recording.Snapshot()
		config := snapshot.Config()
		if config.Model == nil || *config.Model != "parent-model" {
			t.Errorf("fallback model not committed: %+v", config)
		}
		found := false
		for _, event := range snapshot.Events() {
			if update, ok := event.Payload.(*session.SessionUpdatePayload); ok && update.Config != nil && update.Config.Model.Present {
				found = true
			}
		}
		if !found {
			t.Error("missing fallback configuration event")
		}
		checked = true
		return aitest.Says("fallback answer")(ctx, req)
	}
	d := aitest.New(aitest.Fails(errors.New("model_not_found")), fallback)
	e := NewExecutor(continuationProvider{d}, cwd, "parent-model", nil)
	if err := e.SetTypedSessions(store, parent); err != nil {
		t.Fatal(err)
	}
	run, err = e.prepareRun(ctx, tool.AgentExecRequest{Prompt: "work", ParentToolCallID: "launch", Model: "missing-model", TaskID: "test-worker"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.executePreparedRun(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	if !checked || result.Content != "fallback answer" {
		t.Fatalf("fallback not completed: %+v", result)
	}
}
