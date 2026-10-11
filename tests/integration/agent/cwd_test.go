package subagent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/proc"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/tests/integration/testutil"
)

func TestWorkerCwdHandoffAndIsolation(t *testing.T) {
	shell, err := proc.DefaultShell()
	if err != nil {
		t.Fatal(err)
	}
	if shell.Kind != proc.ShellBash {
		t.Skip("Bash cwd tracking")
	}
	cwd := t.TempDir()
	child := filepath.Join(cwd, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	child, err = filepath.EvalSymlinks(child)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "source.go"), []byte("child source"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &testutil.FakeProvider{Responses: []llm.CompletionResponse{
		testutil.ToolCallResponse(tool.ToolBash, "change-cwd", `{"command":"cd child"}`),
		testutil.ToolCallResponse(tool.ToolBash, "inspect-cwd", `{"command":"pwd"}`),
		testutil.ToolCallResponse(tool.ToolRead, "read-relative", `{"file_path":"source.go"}`),
		testutil.EndTurnResponse("Finished"),
		testutil.ToolCallResponse(tool.ToolBash, "sibling-pwd", `{"command":"pwd"}`),
		testutil.EndTurnResponse("Sibling finished"),
	}}
	executor := subagent.NewExecutor(provider, cwd, "fake-model", nil)
	result, err := executor.Run(context.Background(), tool.AgentExecRequest{Mode: "explore", Prompt: "Read source after changing directory", Description: "CWD-WORKER"})
	if err != nil || !result.Success {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	seen := make(map[string]bool)
	for _, message := range result.Messages {
		for _, tr := range message.ToolResults() {
			seen[tr.ToolCallID] = true
			if tr.IsError {
				t.Errorf("%s failed: %s", tr.ToolCallID, tr.Content.Text())
			}
			if tr.ToolCallID == "inspect-cwd" && strings.TrimSpace(tr.Content.Text()) != child {
				t.Errorf("wrong cwd: %q", tr.Content.Text())
			}
			if tr.ToolCallID == "read-relative" && !strings.Contains(tr.Content.Text(), "child source") {
				t.Errorf("wrong relative read: %q", tr.Content.Text())
			}
		}
	}
	for _, id := range []string{"change-cwd", "inspect-cwd", "read-relative"} {
		if !seen[id] {
			t.Errorf("missing result %s", id)
		}
		if effect := tool.PopSideEffect(id); effect != nil {
			t.Errorf("undrained %s: %+v", id, effect)
		}
	}
	initial := provider.Calls[0].SystemPrompt
	for _, call := range provider.Calls[1:4] {
		if !strings.Contains(call.SystemPrompt, "cwd: "+child) {
			t.Errorf("stale environment: %s", call.SystemPrompt)
		}
		if strings.Split(call.SystemPrompt, "<environment>")[0] != strings.Split(initial, "<environment>")[0] {
			t.Error("cwd refresh changed identity or safety constraints")
		}
	}
	sibling, err := executor.Run(context.Background(), tool.AgentExecRequest{Mode: "explore", Prompt: "Check sibling cwd"})
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range sibling.Messages {
		for _, tr := range message.ToolResults() {
			if tr.ToolCallID == "sibling-pwd" && strings.TrimSpace(tr.Content.Text()) != want {
				t.Errorf("worker changed executor/parent cwd: %q", tr.Content.Text())
			}
		}
	}
	if effect := tool.PopSideEffect("sibling-pwd"); effect != nil {
		t.Fatalf("sibling handoff leaked: %+v", effect)
	}
}
