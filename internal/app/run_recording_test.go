package app

import (
	"context"
	"os"
	"path/filepath"
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
