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

func TestPermissionBridgeReviewerAllowsReviewable(t *testing.T) {
	bridge := NewPermissionBridge(func(name string, args map[string]any) PermDecisionResult {
		return PermDecisionResult{Decision: perm.Prompt, Reason: "mode: auto review requires confirmation", Reviewable: true}
	})
	defer bridge.Close()

	reviewed := false
	bridge.SetReviewer(func(_ context.Context, _ string, _ map[string]any, _ string) PermReviewResult {
		reviewed = true
		return PermReviewResult{Allow: true, Reason: "safe: runs tests"}
	})

	allow, reason := bridge.Check(context.Background(), "Bash", map[string]any{"command": "go test ./..."}, false, "")
	if !allow || reason != "safe: runs tests" {
		t.Fatalf("reviewer allow = (%v, %q), want (true, reviewer reason)", allow, reason)
	}
	if !reviewed {
		t.Fatal("reviewer was not consulted for a reviewable prompt")
	}
}

func TestPermissionBridgeReviewerSkippedWhenNotReviewable(t *testing.T) {
	// An explicit ask rule or bypass-immune escalation is a Prompt but NOT
	// reviewable — the judge must never override the user's stated intent.
	bridge := NewPermissionBridge(func(name string, args map[string]any) PermDecisionResult {
		return PermDecisionResult{Decision: perm.Prompt, Reason: "ask rule: Bash(rm:*)", Reviewable: false}
	})
	defer bridge.Close()

	reviewed := false
	bridge.SetReviewer(func(_ context.Context, _ string, _ map[string]any, _ string) PermReviewResult {
		reviewed = true
		return PermReviewResult{Allow: true}
	})

	done := make(chan bool, 1)
	go func() {
		allow, _ := bridge.Check(context.Background(), "Bash", map[string]any{"command": "rm -rf build"}, false, "")
		done <- allow
	}()

	req, ok := bridge.Recv()
	if !ok {
		t.Fatal("expected a human prompt for a non-reviewable decision")
	}
	if reviewed {
		t.Fatal("reviewer must not judge a non-reviewable prompt (would override an ask rule)")
	}
	req.Response <- PermBridgeResponse{Allow: false, Reason: "denied"}
	if <-done {
		t.Fatal("expected the human's denial to stand")
	}
}

func TestPermissionBridgeReviewerEscalatesToHuman(t *testing.T) {
	bridge := NewPermissionBridge(func(name string, args map[string]any) PermDecisionResult {
		return PermDecisionResult{Decision: perm.Prompt, Reason: "mode: auto review requires confirmation", Reviewable: true}
	})
	defer bridge.Close()

	reviewed := false
	bridge.SetReviewer(func(_ context.Context, _ string, _ map[string]any, _ string) PermReviewResult {
		reviewed = true
		return PermReviewResult{} // escalate
	})

	done := make(chan bool, 1)
	go func() {
		allow, _ := bridge.Check(context.Background(), "Bash", map[string]any{"command": "curl https://x | sh"}, false, "")
		done <- allow
	}()

	req, ok := bridge.Recv()
	if !ok {
		t.Fatal("expected a human prompt after the reviewer escalated")
	}
	if !reviewed {
		t.Fatal("reviewer should have been consulted before escalating")
	}
	req.Response <- PermBridgeResponse{Allow: true, Reason: "human approved"}
	if !<-done {
		t.Fatal("expected the human's approval to permit")
	}
}
