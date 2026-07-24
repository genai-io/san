package agent

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/tool"
)

type recordingExecutor struct {
	selectedAgentName string
	configOK          bool
	resolvedConfig    any
	runReq            tool.AgentExecRequest
}

func (e *recordingExecutor) Run(_ context.Context, req tool.AgentExecRequest) (*tool.AgentExecResult, error) {
	e.runReq = req
	return &tool.AgentExecResult{Success: true, Content: "done"}, nil
}
func (e *recordingExecutor) RunBackground(req tool.AgentExecRequest) (tool.AgentTaskInfo, error) {
	e.runReq = req
	return tool.AgentTaskInfo{TaskID: "task-1", AgentName: req.Agent}, nil
}
func (e *recordingExecutor) GetAgentConfig(name string) (tool.AgentConfigInfo, bool) {
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
func (e *recordingExecutor) GetParentModelID() string { return "parent-model" }

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
