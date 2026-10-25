package subagent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestNativeContinuationAfterInferenceFailureKeepsAppliedWork(t *testing.T) {
	store, err := session.NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	path := filepath.Join(cwd, "artifact.txt")
	args, _ := json.Marshal(map[string]string{"file_path": path, "content": "already applied"})
	driver := aitest.New(
		aitest.Asks(ai.ToolCall{ID: "write-once", Name: "Write", Input: string(args)}),
		aitest.Fails(errors.New("simulated non-retryable inference failure")),
		aitest.Says("Continuation verified the existing artifact"),
	)
	executor := NewExecutor(continuationProvider{driver}, cwd, "aitest", nil)
	executor.SetParentPermissionMode(func() PermissionMode { return PermissionAcceptEdits })
	executor.SetSessionStore(store, "failure-parent")
	var tools atomic.Int32
	worker, err := executor.RunBackground(tool.AgentExecRequest{
		Prompt: "Create artifact.txt once, then verify it", ParentToolCallID: "first-call",
		OnToolStart: func(string, string) { tools.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { task.Default().Remove(worker.GetID()) })
	if !worker.WaitForCompletion(5*time.Second) || worker.GetStatus().Status != task.StatusFailed {
		t.Fatalf("first execution did not fail as expected: %+v", worker.GetStatus())
	}
	first := worker.GetStatus()
	if content, err := os.ReadFile(path); err != nil || string(content) != "already applied" {
		t.Fatalf("first execution did not retain side effect: %q, %v", content, err)
	}
	continued, err := executor.RunBackground(tool.AgentExecRequest{
		ResumeTaskID: worker.GetID(), ParentToolCallID: "next-call", Prompt: "Inspect the existing artifact; do not recreate it",
		OnToolStart: func(string, string) { tools.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !continued.WaitForCompletion(5 * time.Second) {
		t.Fatal("continuation hung")
	}
	last := continued.GetStatus()
	if last.Status != task.StatusCompleted || last.AgentSessionID != first.AgentSessionID || last.ID != first.ID || tools.Load() != 1 {
		t.Fatalf("continuation lost identity or replayed tools: %+v, tools=%d", last, tools.Load())
	}
	var history strings.Builder
	for _, message := range driver.Last().Messages {
		history.WriteString(message.Text())
		for _, result := range message.ToolResults() {
			if result.ToolCallID == "write-once" && result.IsError {
				t.Fatal("a real successful tool result was rewritten as a failure")
			}
		}
	}
	for _, want := range []string{"Create artifact.txt once", "Inspect the existing artifact", "simulated non-retryable inference failure"} {
		if !strings.Contains(history.String(), want) {
			t.Fatalf("native continuation lost %q", want)
		}
	}
	state := continued.LaunchState().(*continuationState)
	if state.originalPrompt != "Create artifact.txt once, then verify it" {
		t.Fatal("incremental instruction replaced the original compaction objective")
	}
}

func TestBrokenNativeCheckpointDoesNotStartAnotherAttempt(t *testing.T) {
	store, err := session.NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	driver := aitest.New(aitest.Fails(errors.New("initial failure")))
	executor := NewExecutor(continuationProvider{driver}, t.TempDir(), "aitest", nil)
	executor.SetSessionStore(store, "owner")
	worker, err := executor.RunBackground(tool.AgentExecRequest{Prompt: "Original objective"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { task.Default().Remove(worker.GetID()) })
	if !worker.WaitForCompletion(5 * time.Second) {
		t.Fatal("initial execution hung")
	}
	worker.LaunchState().(*continuationState).persistenceErr = errors.New("native log unavailable")
	if _, err := executor.RunBackground(tool.AgentExecRequest{ResumeTaskID: worker.GetID(), Prompt: "Continue"}); err == nil || !strings.Contains(err.Error(), "inspect/repair") {
		t.Fatalf("broken history was not rejected synchronously: %v", err)
	}
	if info := worker.GetStatus(); info.Attempt != 1 || info.Status != task.StatusFailed || len(info.Attempts) != 1 {
		t.Fatalf("failed continuation request created another terminal notification: %+v", info)
	}
}

func TestNativeContinuationRejectsOverlapAndForeignParent(t *testing.T) {
	store, err := session.NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	driver := aitest.New(aitest.Fails(errors.New("initial failure")), aitest.Hangs())
	executor := NewExecutor(continuationProvider{driver}, t.TempDir(), "aitest", nil)
	executor.SetSessionStore(store, "owner")
	worker, err := executor.RunBackground(tool.AgentExecRequest{Prompt: "Original objective"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Stop(); task.Default().Remove(worker.GetID()) })
	if !worker.WaitForCompletion(5 * time.Second) {
		t.Fatal("initial execution hung")
	}
	foreign := NewExecutor(continuationProvider{driver}, t.TempDir(), "aitest", nil)
	foreign.SetSessionStore(store, "other-owner")
	if _, err := foreign.RunBackground(tool.AgentExecRequest{ResumeTaskID: worker.GetID(), Prompt: "Continue"}); err == nil {
		t.Fatal("foreign parent continued the child session")
	}
	if worker.GetStatus().Attempt != 1 {
		t.Fatal("rejected foreign continuation changed task lifecycle")
	}
	if _, err := executor.RunBackground(tool.AgentExecRequest{ResumeTaskID: worker.GetID(), Prompt: "Continue"}); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.RunBackground(tool.AgentExecRequest{ResumeTaskID: worker.GetID(), Prompt: "Duplicate continuation"}); err == nil {
		t.Fatal("overlapping execution accepted")
	}
	if err := worker.Stop(); err != nil {
		t.Fatal(err)
	}
	if !worker.WaitForCompletion(5 * time.Second) {
		t.Fatal("stopped continuation did not settle")
	}
	if _, err := executor.RunBackground(tool.AgentExecRequest{ResumeTaskID: worker.GetID(), Prompt: "Automatic restart"}); err == nil {
		t.Fatal("deliberately stopped execution resumed")
	}
}
