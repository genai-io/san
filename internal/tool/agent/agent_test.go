package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/tool"
)

type recordingExecutor struct {
	selectedAgentName string
	configOK          bool
	resolvedConfig    any
	runReq            tool.AgentExecRequest
	runResult         *tool.AgentExecResult
	runErr            error
	emptyResult       bool
}

func (e *recordingExecutor) Run(_ context.Context, req tool.AgentExecRequest) (*tool.AgentExecResult, error) {
	e.runReq = req
	if e.runResult != nil || e.runErr != nil || e.emptyResult {
		return e.runResult, e.runErr
	}
	return &tool.AgentExecResult{Success: true, Content: "done"}, nil
}

func TestAgentToolPreservesUnfinishedResult(t *testing.T) {
	for _, cause := range []error{context.Canceled, errors.New("provider unavailable")} {
		t.Run(cause.Error(), func(t *testing.T) {
			executor := &recordingExecutor{
				configOK: true, runErr: cause,
				runResult: &tool.AgentExecResult{
					Content: "partial findings", Error: "execution stopped",
					AgentID: "child-1", OutputFile: "/tmp/child-1.jsonl",
				},
			}
			agentTool := NewAgentTool()
			agentTool.SetExecutor(executor)
			result := agentTool.Execute(context.Background(), map[string]any{"prompt": "Investigate"}, ".")
			if result.Success || !strings.Contains(result.Output, "partial findings") || result.Error != "execution stopped" {
				t.Fatalf("unfinished result lost output or status: %#v", result)
			}
			if !strings.Contains(result.Output, "/tmp/child-1.jsonl") {
				t.Fatalf("model cannot inspect unfinished transcript: %q", result.Output)
			}
			hook, ok := result.HookResponse.(map[string]any)
			if !ok || hook["agentId"] != "child-1" || hook["outputFile"] != "/tmp/child-1.jsonl" || hook["status"] != "error" {
				t.Fatalf("unfinished result lost inspection metadata: %#v", result.HookResponse)
			}
		})
	}
}

func TestAgentToolExecutionErrorWithoutResult(t *testing.T) {
	agentTool := NewAgentTool()
	agentTool.SetExecutor(&recordingExecutor{configOK: true, runErr: errors.New("failed to initialize")})
	result := agentTool.Execute(context.Background(), map[string]any{"prompt": "Investigate"}, ".")
	if result.Success || !strings.Contains(result.Error, "failed to initialize") {
		t.Fatalf("startup failure = %#v", result)
	}
}

func TestAgentToolRejectsMissingResult(t *testing.T) {
	agentTool := NewAgentTool()
	agentTool.SetExecutor(&recordingExecutor{emptyResult: true})
	result := agentTool.Execute(context.Background(), map[string]any{"prompt": "Investigate"}, ".")
	if result.Success || result.Error == "" {
		t.Fatalf("missing executor result must be an error: %#v", result)
	}
}

func TestAgentToolExecutionErrorOverridesSuccess(t *testing.T) {
	agentTool := NewAgentTool()
	agentTool.SetExecutor(&recordingExecutor{
		configOK: true, runErr: errors.New("provider unavailable"),
		runResult: &tool.AgentExecResult{Success: true, Content: "partial findings"},
	})
	result := agentTool.Execute(context.Background(), map[string]any{"prompt": "Investigate"}, ".")
	if result.Success || result.Output != "partial findings" || result.Error != "provider unavailable" {
		t.Fatalf("execution error must win over the result's success flag: %#v", result)
	}
}

func TestAgentToolKeepsDisplayMetadataOutOfModelContext(t *testing.T) {
	agentTool := NewAgentTool()
	agentTool.SetExecutor(&recordingExecutor{runResult: &tool.AgentExecResult{
		Success: true, Content: "Verified finding", Model: "worker-model", ToolUses: 2,
		TotalInputTokens: 100, TotalOutputTokens: 25, Activity: []string{"Read(main.go)"},
		AgentID: "child-1", OutputFile: "/tmp/child-1.jsonl",
	}})
	result := agentTool.Execute(context.Background(), map[string]any{"prompt": "Investigate"}, ".")
	if !result.Success || result.Details == nil {
		t.Fatalf("agent display metadata is missing: %#v", result)
	}
	for _, noise := range []string{"Tokens:", "ToolUses:", "Read(main.go)"} {
		if strings.Contains(result.FormatForLLM(), noise) {
			t.Fatalf("display metadata leaked into model context: %q", result.FormatForLLM())
		}
	}
	for _, evidence := range []string{"Verified finding", "child-1", "/tmp/child-1.jsonl"} {
		if !strings.Contains(result.FormatForLLM(), evidence) {
			t.Fatalf("result lost evidence %q: %q", evidence, result.FormatForLLM())
		}
	}
}
func (e *recordingExecutor) RunBackground(req tool.AgentExecRequest) (tool.AgentTaskInfo, error) {
	e.runReq = req
	return tool.AgentTaskInfo{TaskID: "task-1", AgentName: req.Agent}, nil
}
func (e *recordingExecutor) AgentConfig(name string) (tool.AgentConfigInfo, bool) {
	e.selectedAgentName = name
	if !e.configOK {
		return tool.AgentConfigInfo{}, false
	}
	return tool.AgentConfigInfo{Name: name, PermissionMode: "default"}, true
}
func (e *recordingExecutor) ResolveAgentSelection(name string) (tool.AgentConfigInfo, any, bool) {
	e.selectedAgentName = name
	if !e.configOK {
		return tool.AgentConfigInfo{}, nil, false
	}
	return tool.AgentConfigInfo{Name: name, PermissionMode: "default"}, e.resolvedConfig, true
}
func (e *recordingExecutor) ParentModelID() string { return "parent-model" }

func TestAgentToolUsesOptionalName(t *testing.T) {
	executor := &recordingExecutor{configOK: true}
	agentTool := NewAgentTool()
	agentTool.SetExecutor(executor)
	params := map[string]any{
		"name":        "project-reviewer",
		"description": "Review changes",
		"prompt":      "Inspect the diff",
	}

	if _, err := agentTool.PreparePermission(context.Background(), params, "."); err != nil {
		t.Fatalf("PreparePermission() error: %v", err)
	}
	if executor.selectedAgentName != "project-reviewer" {
		t.Fatalf("config lookup name = %q, want project-reviewer", executor.selectedAgentName)
	}

	result := agentTool.Execute(context.Background(), params, ".")
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}
	if executor.runReq.Agent != "project-reviewer" {
		t.Fatalf("execution agent name = %q, want project-reviewer", executor.runReq.Agent)
	}
}

func TestAgentToolApprovedParamsCarryResolvedConfiguration(t *testing.T) {
	config := &struct{ Name string }{Name: "project-reviewer"}
	executor := &recordingExecutor{configOK: true, resolvedConfig: config}
	agentTool := NewAgentTool()
	agentTool.SetExecutor(executor)
	params := map[string]any{
		"name":   "project-reviewer",
		"prompt": "Inspect the diff",
	}
	if _, err := agentTool.PreparePermission(context.Background(), params, "."); err != nil {
		t.Fatalf("PreparePermission() error: %v", err)
	}

	result := agentTool.ExecuteApproved(context.Background(), params, ".")
	if !result.Success {
		t.Fatalf("ExecuteApproved() failed: %s", result.Error)
	}
	if executor.runReq.ResolvedAgentConfig != config {
		t.Fatalf("execution config = %#v, want approved snapshot %#v", executor.runReq.ResolvedAgentConfig, config)
	}
}

func TestAgentToolOmittedNameStaysEmpty(t *testing.T) {
	executor := &recordingExecutor{configOK: true}
	agentTool := NewAgentTool()
	agentTool.SetExecutor(executor)
	params := map[string]any{"description": "Inspect code", "prompt": "Find references"}

	if _, err := agentTool.PreparePermission(context.Background(), params, "."); err != nil {
		t.Fatalf("PreparePermission() error: %v", err)
	}
	if executor.selectedAgentName != "" {
		t.Fatalf("omitted name lookup = %q, want empty", executor.selectedAgentName)
	}
	agentTool.Execute(context.Background(), params, ".")
	if executor.runReq.Agent != "" {
		t.Fatalf("omitted execution name = %q, want empty", executor.runReq.Agent)
	}
}

func TestAgentToolAcceptsUnknownNameAsDisplayLabel(t *testing.T) {
	fallbackConfig := &struct{ Template string }{Template: "default"}
	executor := &recordingExecutor{configOK: true, resolvedConfig: fallbackConfig}
	agentTool := NewAgentTool()
	agentTool.SetExecutor(executor)
	params := map[string]any{
		"name":              "autopilot-suggestion",
		"description":       "Trace autopilot suggestions",
		"prompt":            "Trace the suggestion flow",
		"run_in_background": true,
	}

	permission, err := agentTool.PreparePermission(context.Background(), params, ".")
	if err != nil {
		t.Fatalf("display-only name should be accepted: %v", err)
	}
	if permission.AgentMeta.AgentName != "autopilot-suggestion" {
		t.Fatalf("permission agent name = %q", permission.AgentMeta.AgentName)
	}

	result := agentTool.ExecuteApproved(context.Background(), params, ".")
	if !result.Success {
		t.Fatalf("background display-only agent failed: %s", result.Error)
	}
	if executor.runReq.Agent != "autopilot-suggestion" || executor.runReq.ResolvedAgentConfig != fallbackConfig {
		t.Fatalf("execution request = %#v", executor.runReq)
	}
}
