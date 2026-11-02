package app

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/filecache"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/ai"
)

type resultHandoffTool struct {
	tool.Tool
	response any
}

func (t resultHandoffTool) Name() string { return "Read" }
func (t resultHandoffTool) Execute(context.Context, map[string]any, string) toolresult.ToolResult {
	return toolresult.ToolResult{Success: true, Output: "visible", HookResponse: t.response}
}

func TestNativeToolResultConsumesHandoffBeforeHook(t *testing.T) {
	engine := hook.NewEngine(setting.NewData(), "session", t.TempDir(), "")
	m := &model{env: env{FileCache: filecache.New()}, services: services{Tool: tool.NewRegistry(), Hook: engine}}
	response := map[string]any{"file": map[string]any{"filePath": "read-file"}}
	id := t.Name()
	adapted := tool.AdaptTool(resultHandoffTool{response: response}, core.ToolSchema{Name: "Read"}, nil)
	result, err := adapted.Run(core.WithToolCallID(t.Context(), id), ai.ToolCall{ID: id, Name: "Read", Input: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tool.PopSideEffect(id) })
	done := make(chan hook.HookInput, 1)
	engine.AddSessionFunctionHook(hook.PostToolUse, "", hook.FunctionHook{Callback: func(_ context.Context, in hook.HookInput) (hook.HookOutput, error) {
		if effect := tool.PopSideEffect(id); effect != nil {
			t.Error("handoff not consumed before hook")
		}
		recent := m.env.FileCache.Recent(1)
		if len(recent) != 1 || recent[0].FilePath != "read-file" {
			t.Errorf("cache before hook = %+v", recent)
		}
		done <- in
		return hook.HookOutput{}, nil
	}})
	tr := core.ToolResult{ToolCallID: id, ToolName: "Read", Content: result.Content}
	if got := m.OnToolResult(tr); !reflect.DeepEqual(*got, tr) {
		t.Fatalf("native result changed: %+v", got)
	}
	select {
	case in := <-done:
		if !reflect.DeepEqual(in.ToolResponse, response) {
			t.Fatalf("response = %#v", in.ToolResponse)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing PostTool hook")
	}
}
