package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/task"
)

func TestAgentNewTaskAcceptsNoSessionPlaceholders(t *testing.T) {
	cases := []struct {
		name  string
		value any
		omit  bool
	}{
		{name: "omitted", omit: true},
		{name: "null", value: nil},
		{name: "empty", value: ""},
		{name: "whitespace", value: "   "},
		{name: "new", value: "new"},
		{name: "new padded", value: " NEW "},
		{name: "dev null", value: "/dev/null"},
	}
	for _, tc := range cases {
		for _, background := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/foreground", true: "/background"}[background], func(t *testing.T) {
				executor := &recordingExecutor{configOK: true}
				agent := NewAgentTool()
				agent.SetExecutor(executor)
				params := map[string]any{
					"name": "refine-file-change-footers", "description": "Refine Edit Write footers",
					"prompt": "Inspect the exact footer assertions, make the focused change, and validate it.", "run_in_background": background,
				}
				if !tc.omit {
					params["resume_task_id"] = tc.value
				}
				permission, err := agent.PreparePermission(context.Background(), params, ".")
				if err != nil {
					t.Fatalf("new-task placeholder blocked approval: %v", err)
				}
				if !strings.HasPrefix(permission.Description, "Spawn ") || permission.AgentMeta.Background != background {
					t.Fatalf("new work presented as continuation: %+v", permission)
				}
				result := agent.ExecuteApproved(context.Background(), params, ".")
				if !result.Success {
					t.Fatalf("new-task placeholder failed execution: %s", result.Error)
				}
				if req := executor.runReq; req.ResumeTaskID != "" || req.ResumeAttempt != 0 || req.Agent != "refine-file-change-footers" || req.Prompt != params["prompt"] || req.Background != background {
					t.Fatalf("placeholder changed task intent: %+v", req)
				}
			})
		}
	}
}

func TestAgentUnknownResumeTargetNeverFallsBackToNewWork(t *testing.T) {
	for _, id := range []string{task.NewID(), "refine-file-change-footers", "/tmp/missing-session", "none", "null"} {
		executor := &recordingExecutor{configOK: true}
		agent := NewAgentTool()
		agent.SetExecutor(executor)
		params := map[string]any{"resume_task_id": id, "description": "Continue existing work", "prompt": "Continue only the unfinished validation"}
		if _, err := agent.PreparePermission(context.Background(), params, "."); err == nil {
			t.Fatalf("unknown target %q was approved as new work", id)
		}
		result := agent.Execute(context.Background(), params, ".")
		if result.Success || executor.runReq.Prompt != "" {
			t.Fatalf("unknown target %q silently launched a new worker: %+v", id, result)
		}
		for _, hint := range []string{"omit resume_task_id or set it to null", "exact Task ID", "No new conversation was started"} {
			if !strings.Contains(result.Error, hint) {
				t.Fatalf("unknown target %q lacks actionable correction %q: %s", id, hint, result.Error)
			}
		}
	}
}
