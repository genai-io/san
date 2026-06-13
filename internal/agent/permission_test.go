package agent

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/tool/perm"
)

func TestPermissionBridgeForcedPromptUsesHookReason(t *testing.T) {
	deciderCalled := false
	bridge := NewPermissionBridge(func(name string, args map[string]any) PermDecisionResult {
		deciderCalled = true
		return PermDecisionResult{Decision: perm.Permit, Reason: "allowed by settings"}
	})
	defer bridge.Close()

	result := make(chan struct {
		allow  bool
		reason string
	}, 1)
	go func() {
		allow, reason := bridge.Check(context.Background(), "Bash", map[string]any{"command": "git status"}, true, "explain this command")
		result <- struct {
			allow  bool
			reason string
		}{allow: allow, reason: reason}
	}()

	req, ok := bridge.Recv()
	if !ok {
		t.Fatal("permission bridge closed unexpectedly")
	}
	if req.ToolName != "Bash" || req.Description != "explain this command" || req.Input["command"] != "git status" {
		t.Fatalf("unexpected permission request: %#v", req)
	}
	req.Response <- PermBridgeResponse{Allow: true, Reason: "approved"}

	got := <-result
	if !got.allow || got.reason != "approved" {
		t.Fatalf("unexpected permission result: %#v", got)
	}
	if deciderCalled {
		t.Fatal("decider must be skipped when a hook forces a prompt (avoids a spurious audit record)")
	}
}
