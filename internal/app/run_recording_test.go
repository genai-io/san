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
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestPrintRecordsNativeConversationAndFinalState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	params, err := buildPrintParams(cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	params.AutoMemory = system.MemoryStores{}
	params.Provider = acpTestProvider{aitest.New(aitest.Says("recorded print")).Client()}
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

func TestPrintShowsStandaloneMaintenanceNotice(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	driver := aitest.New(aitest.Says(memoryMaintenanceProposal))
	params := agent.BuildParams{CWD: t.TempDir(), AutoMemory: system.MemoryStores{Project: t.TempDir()}, Provider: acpTestProvider{driver.Client()}, ModelID: "test", MaxTokens: 64, Tasks: task.NewManager()}
	var displayed strings.Builder
	result, err := runRecordedPrint(t.Context(), params, core.UserMessage(memoryMaintenancePrompt, nil), func(text string) { displayed.WriteString(text) })
	if err != nil || result == nil || result.StopReason != core.StopEndTurn || displayed.String() != memoryMaintenanceNotice {
		t.Fatalf("result=%+v display=%q err=%v", result, displayed.String(), err)
	}
	requests := driver.Sent()
	if len(requests) != 1 || len(requests[0].Tools) != 0 {
		t.Fatalf("expected one tool-free maintenance request: %+v", requests)
	}
}

func TestPrintRecallsBeforeInferenceWithoutTools(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd, notes := t.TempDir(), t.TempDir()
	for path, content := range map[string]string{
		filepath.Join(cwd, "AGENTS.md"):  "PROJECT_INSTRUCTION",
		filepath.Join(notes, "topic.md"): "---\nname: Topic\ndescription: Project preference\ntype: project\n---\nOnly use table tests.",
	} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	driver := aitest.New(aitest.Says(`{"selected_memories":["project/topic.md"]}`), aitest.Says("print answer"))
	params, err := buildPrintParams(cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	params.AutoMemory = system.MemoryStores{Project: notes}
	params.MemoryRecall = setting.MemoryRecallConfig{Mode: setting.MemoryRecallWait}
	params.Provider, params.ModelID, params.MaxTokens = acpTestProvider{driver.Client()}, "test", 64
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
	selector := requests[0].Messages[0].Content.Text()
	if !strings.Contains(selector, "What style?") || strings.Contains(selector, "PROJECT_INSTRUCTION") {
		t.Fatalf("wrong selector input: %q", selector)
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
	_, err := runRecordedPrint(t.Context(), agent.BuildParams{CWD: cwd, Provider: acpTestProvider{driver.Client()}, ModelID: "test", MaxTokens: 64, PermissionRules: func(string, map[string]any) agent.PermDecisionResult {
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
	executor, err := newHeadlessExecutor(acpTestProvider{aitest.New(aitest.Says("headless result")).Client()}, cwd, "test", setting.NewData(), store, registry, pool)
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
	executor, err := newHeadlessExecutor(acpTestProvider{driver.Client()}, cwd, "test", setting.NewData(), store, registry, task.NewManager())
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
