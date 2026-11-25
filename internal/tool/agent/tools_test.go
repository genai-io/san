package agent

import (
	"reflect"
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
				params["tools"] = tc.raw
			}
			approval, err := a.PreparePermission(t.Context(), params, ".")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(approval.AgentMeta.Tools, tc.want) {
				t.Fatalf("preview tools = %#v, want %#v", approval.AgentMeta.Tools, tc.want)
			}
			result := a.ExecuteApproved(t.Context(), params, ".")
			if !result.Success || !reflect.DeepEqual(executor.runReq.Tools, tc.want) {
				t.Fatalf("request tools = %#v, want %#v; result %v", executor.runReq.Tools, tc.want, result.Error)
			}
		}
	}
}

func TestInvalidAgentTools(t *testing.T) {
	for _, bad := range []any{nil, []string(nil), "Read", []any{nil}, []any{1}, []any{"Read", ""}, []any{" Read"}, []any{"Read*"}, []any{"Read", "Read"}} {
		executor := &recordingExecutor{configOK: true}
		a := NewAgentTool()
		a.SetExecutor(executor)
		params := map[string]any{"tools": bad, "prompt": "Inspect"}
		if _, err := a.PreparePermission(t.Context(), params, "."); err == nil {
			t.Errorf("preview accepted %#v", bad)
		}
		if result := a.Execute(t.Context(), params, "."); result.Success || executor.runReq.Prompt != "" {
			t.Errorf("executed invalid selection %#v", bad)
		}
	}
}
