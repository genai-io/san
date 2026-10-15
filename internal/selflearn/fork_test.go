package selflearn

import (
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"

	"context"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
)

// TestTrimTrailingPendingMessages guards against the "messages must
// alternate" provider rejection: when the snapshot ends with a tool_result
// (a RoleUser message with a ToolResult) or any trailing user turn, the fork's
// own UserMessage(prompt) would put two consecutive user-role messages on the wire.
// stubClient wraps a fake driver as the per-turn client the fork is built on.
func stubClient(d ai.Driver) func([]core.Message) (*ai.Client, error) {
	client := ai.NewClientWithDriver(d, ai.Model{ID: "stub", API: "stub"})
	return func([]core.Message) (*ai.Client, error) { return client, nil }
}

func TestTrimTrailingPendingMessages(t *testing.T) {
	asst := core.Message{Role: ai.RoleAssistant, Content: ai.TextContent("ok")}
	usr := core.UserMessage("ask", nil)
	toolResult := core.ToolResultMessage(core.ToolResult{Content: ai.TextContent("tool result")})

	// Snapshot ends with two trailing user messages → both dropped.
	in := []core.Message{asst, usr, usr}
	out := trimTrailingPendingMessages(in)
	if len(out) != 1 || out[0].Role != ai.RoleAssistant {
		t.Fatalf("trailing user not trimmed: %+v", out)
	}
	// Snapshot ends with tool_result(s) → all dropped (provider encodes them
	// as user role and the appended prompt would be the second user turn).
	in = []core.Message{asst, toolResult, toolResult}
	out = trimTrailingPendingMessages(in)
	if len(out) != 1 || out[0].Role != ai.RoleAssistant {
		t.Fatalf("trailing tool_result not trimmed: %+v", out)
	}
	// Snapshot ends with assistant after a trailing user-then-tool mix → all
	// pending tail dropped down to the last assistant message.
	in = []core.Message{asst, usr, toolResult, usr}
	out = trimTrailingPendingMessages(in)
	if len(out) != 1 || out[0].Role != ai.RoleAssistant {
		t.Fatalf("mixed trailing pending not trimmed: %+v", out)
	}
	// Snapshot already ends with assistant → unchanged.
	in = []core.Message{usr, asst}
	out = trimTrailingPendingMessages(in)
	if len(out) != 2 {
		t.Fatalf("non-trailing user incorrectly trimmed: %+v", out)
	}
	// Empty input is safe.
	if got := trimTrailingPendingMessages(nil); got != nil {
		t.Fatalf("nil input should return nil, got %v", got)
	}
}

func TestAllowOnlyPolicy(t *testing.T) {
	tools := core.NewTools(newSkillManageTool(NewSkillManager("/tmp", AllowAllSkillActions())))
	policy := allowOnly(tools)

	if ok, _ := policy(context.Background(), "skill_manage", nil); !ok {
		t.Fatal("skill_manage should be allowed")
	}
	for _, name := range []string{"memory_write", "Bash", "Edit", "Agent", "Read"} {
		if ok, _ := policy(context.Background(), name, nil); ok {
			t.Fatalf("%s must be denied for the reviewer", name)
		}
	}
}

func TestBuildReviewPromptIncludesSkillSection(t *testing.T) {
	mgr := NewSkillManager("/work/project-x", SkillPermissions{AllowCreate: true, AllowUpdate: true})

	prompt := buildReviewPrompt(mgr, "")
	if !strings.Contains(prompt, "skill_manage tool") {
		t.Fatal("prompt should include the skill section")
	}
	if strings.Contains(prompt, "memory_write") {
		t.Fatal("prompt must not mention the removed memory tool")
	}
}

func TestRunReviewWritesSkillAndInheritsSystem(t *testing.T) {
	mgr, _ := newTestSkillManager(t)

	llm := aitest.New(
		aitest.Asks(core.ToolCall{ID: "call-1", Name: "skill_manage", Input: mustJSON(map[string]any{
			"action":      "create",
			"name":        "tabs-formatting",
			"description": "switch files to tab indentation",
			"content":     "Use tabs.",
			"note":        "created tabs skill",
		})}),
		aitest.Says("created tabs-formatting skill"),
	)

	parentSys := core.NewSystem()
	parentSys.Use(core.Section{
		Slot:   core.SlotIdentity,
		Name:   "test-identity",
		Source: core.Predefined,
		Render: func() string { return "PARENT-SYSTEM-MARKER" },
	}, "test")

	fc := ForkConfig{
		Client: stubClient(llm),
		System: parentSys,
		CWD:    "/work/project-x",
		Skills: mgr,
	}
	snapshot := []core.Message{
		core.UserMessage("please switch the file to tabs", nil),
		core.AssistantMessage("done", "", nil),
	}

	summary, err := RunReview(context.Background(), fc, snapshot)
	if err != nil {
		t.Fatalf("RunReview: %v", err)
	}
	if !strings.Contains(summary, "tabs-formatting") {
		t.Fatalf("summary = %q", summary)
	}
	if body, err := mgr.Read("tabs-formatting"); err != nil || !strings.Contains(body, "Use tabs.") {
		t.Fatalf("skill not written: body=%q err=%v", body, err)
	}
	if !strings.Contains(llm.Last().System, "PARENT-SYSTEM-MARKER") {
		t.Fatalf("fork did not inherit the parent system prompt; system=%q", llm.Last().System)
	}
	// The fork must only ever be offered its single write tool.
	if len(llm.Last().Tools) != 1 {
		t.Fatalf("fork tool count = %d, want 1", len(llm.Last().Tools))
	}
}
