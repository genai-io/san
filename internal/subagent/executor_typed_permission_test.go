package subagent

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type childTestResolver struct {
	resolution perm.Resolution
	calls      int
}

func (r *childTestResolver) Resolve(context.Context, string, map[string]any, bool, string) perm.Resolution {
	r.calls++
	return r.resolution
}

func TestTypedChildPreservesOwnerPermissionAuthority(t *testing.T) {
	for _, by := range []string{"mode", "grant", "user", "judge"} {
		t.Run(by, func(t *testing.T) {
			finished := false
			owner := &childTestResolver{resolution: perm.Resolution{Decision: "allow", By: by, Asked: &perm.Asked{}, Finish: func(context.Context, perm.Resolution) error { finished = true; return nil }}}
			policy := childPermission{config: baseAgentConfig(), mode: PermissionDefault, parent: func(*session.Session, bool, tool.AgentInteractionSource) tool.Resolver { return owner }}
			resolution := policy.Resolve(context.Background(), "Read", map[string]any{"file_path": "x"}, false, "")
			if resolution.By != by || resolution.Asked == nil || resolution.Finish == nil || owner.calls != 1 {
				t.Fatalf("authority lost: %+v", resolution)
			}
			if err := resolution.Finish(context.Background(), resolution); err != nil || !finished {
				t.Fatal("finish lost")
			}
		})
	}
}

func TestTypedSourceDoesNotCitePreviousAttempt(t *testing.T) {
	// A failed continuation can expose the previous answer as partial content.
	// Its native ID exists, but no current execution admitted that message.
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
	e := NewExecutor(continuationProvider{aitest.New(aitest.Says("same answer"))}, cwd, "aitest", nil)
	if err := e.SetSessions(store, parent); err != nil {
		t.Fatal(err)
	}
	first, err := e.Run(ctx, tool.AgentExecRequest{Prompt: "first", ParentToolCallID: "launch"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.Open(ctx, first.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	snapshot := child.Snapshot()
	events := snapshot.Events()
	run := &preparedRun{cfg: &runConfig{config: baseAgentConfig()}, recording: child, sourceAfter: events[len(events)-1].Seq}
	failed := e.finalizeResult(run, &core.Result{Content: "same answer", Messages: first.Messages, StopReason: core.StopError}, false, "continuation failed")
	if failed.SourceMessageSeq != 0 {
		t.Fatalf("cited earlier attempt: %d", failed.SourceMessageSeq)
	}
}
