package agent

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/tool/perm"
)

func TestForcePromptFuncBypassesDecider(t *testing.T) {
	bridge := NewPermissionBridge(func(name string, args map[string]any) PermDecisionResult {
		t.Fatal("decider should not run for a forced prompt")
		return PermDecisionResult{Decision: perm.Permit}
	})
	defer bridge.Close()

	type promptResult struct {
		allow  bool
		reason string
	}
	result := make(chan promptResult, 1)
	go func() {
		allow, reason := bridge.ForcePromptFunc()(context.Background(), "Bash", map[string]any{"command": "git push"}, "requested by PreToolUse hook")
		result <- promptResult{allow: allow, reason: reason}
	}()

	req, ok := bridge.Recv()
	if !ok {
		t.Fatal("permission bridge closed unexpectedly")
	}
	if req.ToolName != "Bash" || req.Input["command"] != "git push" {
		t.Fatalf("unexpected permission request: %#v", req)
	}
	if req.Description != "requested by PreToolUse hook" {
		t.Fatalf("prompt description = %q, want hook reason", req.Description)
	}
	req.Response <- PermBridgeResponse{Allow: true, Reason: "approved"}

	got := <-result
	if !got.allow || got.reason != "approved" {
		t.Fatalf("unexpected prompt result: %#v", got)
	}
}
