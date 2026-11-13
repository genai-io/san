package agent

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/tool/perm"
)

func TestResolvedPermissionAuthority(t *testing.T) {
	for _, by := range []string{"config", "mode", "grant"} {
		gate := NewPermissionGate(func(string, map[string]any) PermDecisionResult {
			return PermDecisionResult{Decision: perm.Permit, By: by, Scope: "session", Reason: "not authority"}
		})
		r := gate.Resolve(context.Background(), "Bash", nil, false, "")
		gate.Close()
		if r.Decision != "allow" || r.By != by || r.Scope != "session" {
			t.Fatalf("%+v", r)
		}
	}
	gate := NewPermissionGate(func(string, map[string]any) PermDecisionResult {
		return PermDecisionResult{Decision: perm.Prompt, Judgeable: true}
	})
	defer gate.Close()
	gate.SetJudge(func(context.Context, string, map[string]any, string) PermJudgeResult {
		return PermJudgeResult{Allow: true, Reason: "reviewed"}
	})
	if r := gate.Resolve(context.Background(), "Bash", nil, false, ""); r.By != "judge" || r.Decision != "allow" {
		t.Fatalf("%+v", r)
	}
}

func TestResolvedPromptReplyAndCancellation(t *testing.T) {
	gate := NewPermissionGate(func(string, map[string]any) PermDecisionResult {
		return PermDecisionResult{Decision: perm.Prompt, Judgeable: true}
	})
	defer gate.Close()
	// A nonallow judge must escalate rather than manufacture a denial.
	gate.SetJudge(func(context.Context, string, map[string]any, string) PermJudgeResult { return PermJudgeResult{} })
	result := make(chan perm.Resolution, 1)
	go func() { result <- gate.Resolve(context.Background(), "Bash", nil, false, "") }()
	req, ok := gate.Recv()
	if !ok {
		t.Fatal("no prompt")
	}
	req.Response <- PermGateResponse{Cancelled: true, By: "user", Asked: &perm.Asked{Options: []string{"Allow", "Deny"}, At: 123}}
	r := <-result
	if r.Decision != "cancelled" || r.By != "user" || r.Asked == nil || r.Asked.At != 123 {
		t.Fatalf("%+v", r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := gate.Resolve(ctx, "Bash", nil, false, ""); r.Decision != "cancelled" {
		t.Fatalf("%+v", r)
	}
}
