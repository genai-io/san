package agent

import (
	"reflect"
	"strings"
	"testing"
)

func TestAgentToolSelection(t *testing.T) {
	for _, tc := range []struct {
		raw  any
		want []string
	}{{nil, nil}, {[]any{}, []string{}}, {[]any{"Read", "mcp__service__lookup"}, []string{"Read", "mcp__service__lookup"}}, {[]string{"Write"}, []string{"Write"}}} {
		for _, bg := range []bool{false, true} {
			executor := &recordingExecutor{configOK: true}
			a := NewAgentTool()
			a.SetExecutor(executor)
			params := map[string]any{"prompt": "Review", "run_in_background": bg}
			if tc.raw != nil {
				params["allow_tools"] = tc.raw
				params["deny_tools"] = []any{"Bash"}
			}
			approval, err := a.PreparePermission(t.Context(), params, ".")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(approval.AgentMeta.Tools, tc.want) {
				t.Fatalf("preview tools = %#v, want %#v", approval.AgentMeta.Tools, tc.want)
			}
			result := a.ExecuteApproved(t.Context(), params, ".")
			var wantDeny []string
			if tc.raw != nil {
				wantDeny = []string{"Bash"}
			}
			if !result.Success || !reflect.DeepEqual(executor.runReq.AllowTools, tc.want) || !reflect.DeepEqual(executor.runReq.DenyTools, wantDeny) {
				t.Fatalf("allow=%#v deny=%#v error=%v", executor.runReq.AllowTools, executor.runReq.DenyTools, result.Error)
			}
		}
	}
}

func TestInvalidAgentTools(t *testing.T) {
	for _, field := range []string{"allow_tools", "deny_tools"} {
		for _, bad := range []any{nil, []string(nil), "Read", []any{nil}, []any{1}, []any{"Read", ""}, []any{" Read"}, []any{"Read*"}, []any{"Read", "Read"}} {
			executor := &recordingExecutor{configOK: true}
			a := NewAgentTool()
			a.SetExecutor(executor)
			params := map[string]any{field: bad, "prompt": "Inspect"}
			if _, err := a.PreparePermission(t.Context(), params, "."); err == nil {
				t.Errorf("preview accepted %s=%#v", field, bad)
			}
			if result := a.Execute(t.Context(), params, "."); result.Success || executor.runReq.Prompt != "" {
				t.Errorf("executed invalid %s=%#v", field, bad)
			}
		}
	}
}

func TestAgentRejectsDeprecatedTools(t *testing.T) {
	executor := &recordingExecutor{configOK: true}
	a := NewAgentTool()
	a.SetExecutor(executor)
	for _, params := range []map[string]any{{"prompt": "Inspect", "tools": []any{}}, {"prompt": "Inspect", "tools": nil, "allow_tools": []any{"Read"}}} {
		if _, err := a.PreparePermission(t.Context(), params, "."); err == nil || !strings.Contains(err.Error(), "no longer accepts tools") {
			t.Fatalf("preview error: %v", err)
		}
		if result := a.Execute(t.Context(), params, "."); result.Success || !strings.Contains(result.Error, "no longer accepts tools") || executor.runReq.Prompt != "" {
			t.Fatalf("execution error: %v", result.Error)
		}
	}
}
