package subagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type childJudgeResolver struct{ child *session.Session }

func (r childJudgeResolver) Resolve(ctx context.Context, _ string, _ map[string]any, _ bool, _ string) perm.Resolution {
	scope := r.child.Completion(session.PurposeJudge)
	finish, err := scope.Admission()(ctx, "aitest", ai.Model{ID: "judge"}, &ai.Request{System: "judge policy"})
	if err != nil {
		return perm.Resolution{Err: err}
	}
	if err := finish(ctx, &ai.Response{Usage: ai.Usage{Input: 17, Output: 4}}, nil); err != nil {
		return perm.Resolution{Err: err}
	}
	return perm.Resolution{Decision: "allow", By: "judge", Finish: func(ctx context.Context, resolution perm.Resolution) error {
		if err := scope.FinishPermission(ctx, resolution); err != nil {
			return err
		}
		return scope.Close()
	}}
}

func TestTypedPermissionFactoryBindsJudgeToChild(t *testing.T) {
	ctx := context.Background()
	cwd := t.TempDir()
	path := filepath.Join(cwd, "evidence.txt")
	if err := os.WriteFile(path, []byte("evidence"), 0600); err != nil {
		t.Fatal(err)
	}
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
	args, _ := json.Marshal(map[string]string{"file_path": path})
	d := aitest.New(aitest.Asks(ai.ToolCall{ID: "read", Name: "Read", Input: string(args)}), aitest.Says("done"))
	e := NewExecutor(continuationProvider{d}, cwd, "aitest", nil)
	if err := e.SetSessions(store, parent); err != nil {
		t.Fatal(err)
	}
	var captured *session.Session
	e.ConfigureOwner(nil, Default(), func(child *session.Session, _ bool, _ tool.AgentInteractionSource) tool.Resolver {
		captured = child
		return childJudgeResolver{child}
	}, nil)
	result, err := e.Run(ctx, tool.AgentExecRequest{Prompt: "read evidence", ParentToolCallID: "launch"})
	if err != nil {
		t.Fatal(err)
	}
	if captured == nil || captured == parent || captured.ID() != result.AgentID {
		t.Fatal("factory did not receive child owner")
	}
	snapshot := parent.Snapshot()
	if len(snapshot.Events()) != 1 {
		t.Fatal("child judge wrote parent")
	}
	snapshot = captured.Snapshot()
	events := snapshot.Events()
	found := false
	for i, event := range events {
		if permission, ok := event.Payload.(*session.PermissionDecidePayload); ok {
			if permission.By != "judge" || event.Source == nil || event.Source.Seq == 0 || (event.Source.Session != "" && event.Source.Session != captured.ID()) {
				t.Fatalf("missing child judge authority: %+v", event)
			}
			if i+1 >= len(events) {
				t.Fatal("missing judge finish")
			}
			finish, ok := events[i+1].Payload.(*session.InferenceFinishPayload)
			if !ok || finish.Usage == nil || finish.Usage.Input != 17 || finish.Usage.Output != 4 {
				t.Fatalf("missing judge accounting: %+v", events[i+1])
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no child judge decision")
	}
}
