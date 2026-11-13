package subagent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestTypedCwdHandoffPersistsBeforeContinuation(t *testing.T) {
	ctx := context.Background()
	cwd := t.TempDir()
	target, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := session.OpenStore(ctx, cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	parent, err := store.Create(ctx, session.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	args, _ := json.Marshal(map[string]any{"command": "cd '" + target + "'"})
	d := aitest.New(aitest.Asks(ai.ToolCall{ID: "change-cwd", Name: "Bash", Input: string(args)}), aitest.Says("continued"))
	e := NewExecutor(continuationProvider{d}, cwd, "aitest", nil)
	if err := e.SetTypedSessions(store, parent); err != nil {
		t.Fatal(err)
	}
	a, err := e.RunBackground(tool.AgentExecRequest{Prompt: "change directory", ParentToolCallID: "launch", MaxSteps: 1, ResolvedAgentConfig: &AgentConfig{PermissionMode: PermissionBypass}})
	if err != nil {
		t.Fatal(err)
	}
	if !a.WaitForCompletion(5 * time.Second) {
		t.Fatal("hung")
	}
	child, err := store.Open(ctx, a.GetStatus().AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := child.Snapshot()
	state := snapshot.State()
	child.Close()
	if state.ExecutionCWD == nil || *state.ExecutionCWD != target {
		t.Fatalf("cwd not persisted: %+v; status=%+v", state, a.GetStatus())
	}
	b, err := e.RunBackground(tool.AgentExecRequest{ResumeTaskID: a.GetID(), Prompt: "continue", ParentToolCallID: "later", MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !b.WaitForCompletion(5 * time.Second) {
		t.Fatal("hung")
	}
	if status := b.GetStatus(); status.Error != "" {
		t.Fatalf("continuation: %+v", status)
	}
	if !strings.Contains(d.Last().System, "cwd: "+target) {
		t.Fatalf("continuation environment did not retain cwd: %s", d.Last().System)
	}
}
