package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestPrintRecordsNativeConversationAndFinalState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	params, err := buildPrintParams(cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	params.AutoMemory = agent.AutoMemoryConfig{}
	params.Provider = staticTestProvider{aitest.New(aitest.Says("recorded print")).Client()}
	params.ModelID = "test"
	params.Tasks = task.NewManager()
	result, err := runRecordedPrint(t.Context(), params, core.UserMessage("print question", nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Content != "recorded print" {
		t.Fatalf("result=%+v", result)
	}
	store, err := session.OpenStore(t.Context(), cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	page, err := store.List(t.Context(), session.ListQuery{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("sessions=%+v err=%v", page, err)
	}
	projection, err := store.Read(t.Context(), page.Items[0].SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Messages()) != 2 || projection.Config().Model == nil || *projection.Config().Model != "test" || projection.State().ExecutionCWD == nil || *projection.State().ExecutionCWD != cwd {
		t.Fatalf("incomplete print projection: %+v", projection)
	}
	assertRecordedOneShot(t, projection)
	owner, err := store.Open(t.Context(), projection.SessionID())
	if err != nil {
		t.Fatalf("print owner not closed: %v", err)
	}
	_ = owner.Close()
}

func TestPrintMemoryLanguageReachesMain(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const prompt = "remember: Prefer table tests."
	const answer = "main response"
	driver := aitest.New(aitest.Says(answer))
	params := agent.BuildParams{CWD: t.TempDir(), AutoMemory: agent.AutoMemoryConfig{Store: system.MemoryStore(t.TempDir())}, Provider: staticTestProvider{driver.Client()}, ModelID: "test", MaxTokens: 64, Tasks: task.NewManager(), PermissionRules: (agent.PermissionPolicy{}).Decide}
	var displayed strings.Builder
	result, err := runRecordedPrint(t.Context(), params, core.UserMessage(prompt, nil), func(text string) { displayed.WriteString(text) })
	if err != nil || result == nil || result.StopReason != core.StopEndTurn || displayed.String() != answer || driver.Calls() != 1 {
		t.Fatalf("result=%+v display=%q calls=%d err=%v", result, displayed.String(), driver.Calls(), err)
	}
}

func TestPrintUpdatesProjectInstructionsWithoutMemoryUpdate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd, notes := t.TempDir(), t.TempDir()
	instructions := filepath.Join(cwd, system.InstructionFile)
	index := filepath.Join(notes, system.AutoMemoryIndexName)
	const original = "Use make for builds.\n"
	const updated = "Use ninja for builds.\n"
	const indexText = "# Private memory index\n"
	for path, content := range map[string]string{instructions: original, index: indexText} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	driver := aitest.New(
		aitest.Asks(ai.ToolCall{ID: "read", Name: "Read", Input: `{"file_path":"AGENTS.md"}`}),
		aitest.Asks(ai.ToolCall{ID: "edit", Name: "Edit", Input: `{"file_path":"AGENTS.md","old_string":"Use make for builds.","new_string":"Use ninja for builds."}`}),
		aitest.Says("Updated the repository instructions."),
	)
	params, err := buildPrintParams(cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	policy := agent.PermissionPolicy{Settings: setting.NewData(), Session: &setting.SessionPermissions{Mode: setting.ModeAutoAccept, ShouldAvoidPrompts: true}}
	params.PermissionRules = policy.Decide
	params.AutoMemory = agent.AutoMemoryConfig{Store: system.MemoryStore(notes)}
	params.Provider, params.ModelID, params.Tasks = staticTestProvider{driver.Client()}, "test", task.NewManager()
	const prompt = "请记住：以后用 ninja 构建，更新项目的 AGENTS.md。"
	if result, err := runRecordedPrint(t.Context(), params, core.UserMessage(prompt, nil), nil); err != nil || result == nil || result.StopReason != core.StopEndTurn {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	requests := driver.Sent()
	if len(requests) != 3 || len(requests[0].Tools) == 0 {
		t.Fatalf("expected only the normal main tool loop, got %d requests", len(requests))
	}
	first := requests[0].Messages[0].Text()
	if !strings.HasPrefix(first, prompt) || !strings.Contains(first, original) || strings.Contains(first, indexText) {
		t.Fatalf("unexpected main input: %q", first)
	}
	for path, want := range map[string]string{instructions: updated, index: indexText} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v; want %q (tool result: %+v)", path, got, err, want, requests[2].Messages[len(requests[2].Messages)-1].ToolResults())
		}
	}
}

func TestPrintRecallsBeforeInferenceWithoutTools(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd, notes := t.TempDir(), t.TempDir()
	for path, content := range map[string]string{
		filepath.Join(cwd, "AGENTS.md"):   "PROJECT_INSTRUCTION",
		filepath.Join(notes, "topic.md"):  "---\nname: Topic\ndescription: Project preference\ntype: project\n---\nOnly use table tests.",
		filepath.Join(notes, "MEMORY.md"): "- [Topic](topic.md)\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	driver := aitest.New(aitest.Says(`{"selected_memories":["topic.md"]}`), aitest.Says("print answer"))
	params, err := buildPrintParams(cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	params.AutoMemory = agent.AutoMemoryConfig{
		Store:  system.MemoryStore(notes),
		Recall: setting.MemoryRecallConfig{Mode: setting.MemoryRecallWait},
	}
	params.Provider, params.ModelID, params.MaxTokens = staticTestProvider{driver.Client()}, "test", 64
	params.Tasks = task.NewManager()
	for _, schema := range params.Schemas() {
		params.DisabledTools[schema.Name] = true
	}
	var displayed strings.Builder
	result, err := runRecordedPrint(t.Context(), params, core.UserMessage("What style?", nil), func(text string) { displayed.WriteString(text) })
	if err != nil || result == nil || result.Content != "print answer" || displayed.String() != "print answer" {
		t.Fatalf("result=%+v display=%q err=%v", result, displayed.String(), err)
	}
	requests := driver.Sent()
	if len(requests) != 2 || len(requests[0].Tools) != 0 || len(requests[1].Tools) != 0 {
		t.Fatalf("expected a tool-free selector followed by tool-free main inference: %+v", requests)
	}
	selector := requests[0].Messages
	if len(selector) != 3 || !strings.Contains(selector[0].Text(), "topic.md") || selector[2].Text() != "What style?" {
		t.Fatalf("wrong selector input: %+v", selector)
	}
	main := requests[1].Messages[0].Content.Text()
	for _, want := range []string{"What style?", "Only use table tests.", "PROJECT_INSTRUCTION"} {
		if !strings.Contains(main, want) {
			t.Fatalf("missing %q in main input: %q", want, main)
		}
	}
	if strings.Contains(main, notes) || strings.Contains(main, "memory-auto") {
		t.Fatalf("legacy store path or index injected: %q", main)
	}
}

func TestPrintStorageFailurePreventsModelInvocation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	// A regular file cannot be used as the home-backed session library.
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".san"), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	driver := aitest.New(aitest.Says("must not run"))
	_, err := runRecordedPrint(t.Context(), agent.BuildParams{CWD: cwd, Provider: staticTestProvider{driver.Client()}, ModelID: "test", MaxTokens: 64, PermissionRules: func(string, map[string]any) agent.PermDecisionResult {
		return agent.PermDecisionResult{Decision: perm.Reject, By: "config"}
	}}, core.UserMessage("fail closed", nil), nil)
	if err == nil || driver.Calls() != 0 {
		t.Fatalf("err=%v calls=%d", err, driver.Calls())
	}
}

func TestHeadlessAgentRecordsTopLevelNativeSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	store, err := session.OpenStore(t.Context(), cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pool := task.NewManager()
	defer func() { pool.Cancel(); pool.Join() }()
	registry, err := subagent.Load(cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := newHeadlessExecutor(staticTestProvider{aitest.New(aitest.Says("headless result")).Client()}, cwd, "test", setting.NewData(), store, registry, pool)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Run(t.Context(), tool.AgentExecRequest{Prompt: "standalone work", MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Content != "headless result" || result.TranscriptPath == "" {
		t.Fatalf("result=%+v", result)
	}
	page, err := store.List(t.Context(), session.ListQuery{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("sessions=%+v err=%v", page, err)
	}
	projection, err := store.Read(t.Context(), page.Items[0].SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Identity().Parent != nil {
		t.Fatal("standalone run received a fabricated parent")
	}
	assertRecordedOneShot(t, projection)
	owner, err := store.Open(t.Context(), projection.SessionID())
	if err != nil {
		t.Fatalf("headless writer not drained/closed: %v", err)
	}
	_ = owner.Close()
}

func TestHeadlessAgentClosedStorePreventsModelInvocation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	store, err := session.OpenStore(t.Context(), cwd)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	registry, err := subagent.Load(cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	driver := aitest.New(aitest.Says("must not run"))
	executor, err := newHeadlessExecutor(staticTestProvider{driver.Client()}, cwd, "test", setting.NewData(), store, registry, task.NewManager())
	if err == nil {
		_, err = executor.Run(context.Background(), tool.AgentExecRequest{Prompt: "fail closed"})
	}
	if err == nil || driver.Calls() != 0 {
		t.Fatalf("err=%v calls=%d", err, driver.Calls())
	}
}

func assertRecordedOneShot(t *testing.T, projection session.Projection) {
	t.Helper()
	var starts, finishes int
	for _, event := range projection.Events() {
		switch event.Payload.(type) {
		case *session.InferenceStartPayload:
			starts++
		case *session.InferenceFinishPayload:
			finishes++
		}
	}
	if starts != 1 || finishes != 1 {
		t.Fatalf("inference starts=%d finishes=%d", starts, finishes)
	}
	unfinished := projection.Unfinished()
	if len(unfinished.Inferences) != 0 {
		t.Fatalf("completed run left unfinished inference: %+v", unfinished)
	}
}

type staticTestProvider struct{ client *ai.Client }

func (p staticTestProvider) Client(string, map[string]string) (*ai.Client, error) {
	return p.client, nil
}
func (staticTestProvider) Name() string { return "static-test" }
func (staticTestProvider) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{{ID: "test", ContextWindow: 200000}}, nil
}
