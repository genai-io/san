package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestMainApprovalCarriesExecutionContext(t *testing.T) {
	m := interactionModel()
	m.env.CWD = t.TempDir()
	m.env.SessionPermissions = setting.NewSessionPermissions()
	request, err := m.preparePermissionRequest(&agent.PermGateRequest{RequestID: "main-context", ToolName: "Bash", Input: map[string]any{"command": "touch fixture.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if request.WorkingDirectory != m.env.CWD || request.PermissionMode != m.env.SessionMode() {
		t.Fatalf("main approval lost execution context: cwd=%q mode=%q", request.WorkingDirectory, request.PermissionMode)
	}
	if request.ID != "main-context" || request.BashMeta == nil || request.BashMeta.Command != "touch fixture.txt" {
		t.Fatal("execution context displaced the rich tool preview or request identity")
	}
}

func TestMainPermissionBoundaryRejectsInvalidShellBeforeApproval(t *testing.T) {
	for _, tc := range []struct {
		name, input, reason string
	}{
		{"mode conflict", `{"command":"touch should-not-run-mode-conflict","execution_mode":"foreground","run_in_background":true}`, "execution_mode conflicts with run_in_background"},
		{"service timeout", `{"command":"touch should-not-run-mode-conflict","execution_mode":"background_service","timeout_ms":1000}`, "background_service cannot specify an execution timeout"},
		{"invalid timeout", `{"command":"touch should-not-run-mode-conflict","timeout_ms":0}`, "timeout_ms must be a positive integer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := interactionModel()
			m.services.Agent = &agent.Runtime{}
			m.env.CWD = t.TempDir()
			gate := agent.NewPermissionGate(func(string, map[string]any) agent.PermDecisionResult {
				return agent.PermDecisionResult{Decision: perm.Prompt, Reason: "mode: default requires confirmation"}
			})
			defer gate.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx = core.WithToolCallID(ctx, "invalid-shell-call")
			type outcome struct {
				decision sdkagent.Decision
				err      error
			}
			done := make(chan outcome, 1)
			go func() {
				decision, err := tool.HookedPermission(nil, gate)(ctx, sdkagent.PreToolContext{
					Call: ai.ToolCall{ID: "invalid-shell-call", Name: "Bash", Input: tc.input},
				})
				done <- outcome{decision, err}
			}()
			request, ok := gate.Recv()
			if !ok {
				t.Fatal("permission boundary did not receive the call")
			}
			m.HandlePermGate(request)
			select {
			case got := <-done:
				if got.err != nil || !got.decision.Block || !strings.Contains(got.decision.Reason, tc.reason) {
					t.Fatalf("invalid call lost actionable error: %+v / %v", got.decision, got.err)
				}
			case <-ctx.Done():
				t.Fatal("invalid call waited for human approval")
			}
			if m.interactions.active != nil || len(m.interactions.queue) != 0 || m.interactions.shown {
				t.Fatal("invalid call reached the human interaction queue")
			}
			if _, err := os.Stat(filepath.Join(m.env.CWD, "should-not-run-mode-conflict")); !os.IsNotExist(err) {
				t.Fatalf("invalid command produced a side effect: %v", err)
			}
		})
	}
}

func TestInvalidQueuedApprovalDoesNotHideNextRequest(t *testing.T) {
	m := interactionModel()
	m.env.CWD = t.TempDir()
	invalid := interactionApproval(m, context.Background(), "invalid")
	invalid.PermissionView = nil
	invalid.Permission.ToolName = "Bash"
	invalid.Permission.Input = map[string]any{
		"command": "touch should-not-run-mode-conflict", "execution_mode": "foreground", "run_in_background": true,
	}
	next := interactionApproval(m, context.Background(), "valid")
	m.interactions.queue = append(m.interactions.queue, invalid, next)
	m.advanceInteractions()

	select {
	case response := <-invalid.Permission.Response:
		if response.Allow || !strings.Contains(response.Reason, "execution_mode conflicts") {
			t.Fatalf("invalid queued call lost its validation reason: %+v", response)
		}
	default:
		t.Fatal("invalid queued call still waits for human approval")
	}
	if m.interactions.active != next || !m.interactions.shown || !m.userInput.Approval.IsActive() || len(m.interactions.queue) != 0 {
		t.Fatal("invalid approval stalled the valid FIFO successor")
	}
}

func TestPermDecisionRecordSnapshotsInput(t *testing.T) {
	input := map[string]any{"prompt": "inspect the repository"}
	req := &agent.PermGateRequest{
		RequestID: "permission-1",
		ToolName:  "Agent",
		Input:     input,
	}

	record := permDecisionRecord(req, permissionDecision{
		Approved: true,
		Request:  &perm.PermissionRequest{ToolName: "Agent"},
	}, "user approved", "normal")

	// Agent execution decorates this same input map after the permission gate
	// opens. The permission record must already own serialized bytes by then.
	input["_onActivity"] = func() {}
	input["prompt"] = "changed after approval"

	if got, want := string(record.Input), `{"prompt":"inspect the repository"}`; got != want {
		t.Fatalf("permission input snapshot = %s, want %s", got, want)
	}
}
