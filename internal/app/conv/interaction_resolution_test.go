package conv

import (
	"context"
	"testing"
	"time"

	"github.com/genai-io/san/internal/interaction"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
)

func TestResolveApprovalPreservesNativeQuestionAndAuthority(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "approved", true: "cancelled"}[cancelled], func(t *testing.T) {
			bridge := NewAgentToUI(1)
			defer bridge.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			source := tool.AgentInteractionSource{AgentID: "child", TaskID: "child", ParentToolCallID: "parent-call", ToolCallID: "child-call", WorkingDirectory: "/worker", PermissionMode: "normal"}
			view := &perm.PermissionRequest{ID: "approval", ToolName: "Write", OnceOnly: true}
			done := make(chan perm.Resolution, 1)
			go func() { done <- bridge.ResolveApproval(ctx, source, view, map[string]any{"file_path": "a"}) }()
			req := receiveInteraction(t, bridge)
			if req.Source != source || req.Permission.ToolCallID != "child-call" {
				t.Fatal("source identity lost")
			}
			p := interaction.NewPermission(req, bridge, view, func(*perm.PermissionRequest) []string { return []string{"Allow once", "Deny"} })
			if p == nil {
				t.Fatal("approval was not displayed")
			}
			if cancelled {
				cancel()
			} else if !p.Resolve(bridge, interaction.PermissionDecision{Approved: true, Request: view}, nil, nil) {
				t.Fatal("reply rejected")
			}
			select {
			case got := <-done:
				expected := "allow"
				if cancelled {
					expected = "cancelled"
				}
				if got.Decision != expected || got.By != "user" || got.Asked == nil || got.Asked.Options[0] != "Allow once" || len(got.Asked.Detail) == 0 {
					t.Fatalf("resolution=%+v", got)
				}
				if !cancelled && got.Scope != "once" {
					t.Fatalf("scope=%s", got.Scope)
				}
			case <-time.After(time.Second):
				t.Fatal("approval did not finish")
			}
		})
	}
}
