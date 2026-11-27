package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/runtime"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestMainApprovalCarriesExecutionContext(t *testing.T) {
	m := userPromptModel()
	m.env.CWD = t.TempDir()
	m.env.SessionPermissions = setting.NewSessionPermissions()
	request, err := m.preparePermissionRequest(&runtime.PermGateRequest{RequestID: "main-context", ToolName: "Bash", Input: map[string]any{"command": "touch fixture.txt"}})
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
			m := userPromptModel()
			m.services.Agent = &runtime.Runtime{}
			m.env.CWD = t.TempDir()
			gate := runtime.NewPermissionGate(func(string, map[string]any) runtime.PermDecisionResult {
				return runtime.PermDecisionResult{Decision: perm.Prompt, Reason: "mode: default requires confirmation"}
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
			if m.userPrompts.active != nil || len(m.userPrompts.queue) != 0 || m.userPrompts.shown {
				t.Fatal("invalid call reached the human interaction queue")
			}
			if _, err := os.Stat(filepath.Join(m.env.CWD, "should-not-run-mode-conflict")); !os.IsNotExist(err) {
				t.Fatalf("invalid command produced a side effect: %v", err)
			}
		})
	}
}

func TestInvalidQueuedApprovalDoesNotHideNextRequest(t *testing.T) {
	m := userPromptModel()
	m.env.CWD = t.TempDir()
	invalid := userPromptApproval(m, context.Background(), "invalid")
	invalid.PermissionView = nil
	invalid.Permission.ToolName = "Bash"
	invalid.Permission.Input = map[string]any{
		"command": "touch should-not-run-mode-conflict", "execution_mode": "foreground", "run_in_background": true,
	}
	next := userPromptApproval(m, context.Background(), "valid")
	m.userPrompts.queue = append(m.userPrompts.queue, invalid, next)
	m.advanceUserPrompts()

	select {
	case response := <-invalid.Permission.Response:
		if response.Allow || !strings.Contains(response.Reason, "execution_mode conflicts") {
			t.Fatalf("invalid queued call lost its validation reason: %+v", response)
		}
	default:
		t.Fatal("invalid queued call still waits for human approval")
	}
	if m.userPrompts.active != next || !m.userPrompts.shown || !m.userInput.Approval.IsActive() || len(m.userPrompts.queue) != 0 {
		t.Fatal("invalid approval stalled the valid FIFO successor")
	}
}
