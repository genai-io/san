package subagent_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/gen-code/internal/core"
	"github.com/genai-io/gen-code/internal/hook"
	"github.com/genai-io/gen-code/internal/llm"
	"github.com/genai-io/gen-code/internal/setting"
	"github.com/genai-io/gen-code/internal/subagent"
	"github.com/genai-io/gen-code/internal/task"
	"github.com/genai-io/gen-code/internal/tool/perm"
	_ "github.com/genai-io/gen-code/internal/tool/registry"
	"github.com/genai-io/gen-code/tests/integration/testutil"
)

func TestAgent_GeneralExploreMode(t *testing.T) {
	mp := &testutil.MockProvider{
		Responses: []llm.CompletionResponse{
			{
				Content: "Explored the codebase", StopReason: "end_turn",
				Usage: llm.Usage{InputTokens: 50, OutputTokens: 25},
			},
		},
	}

	executor := subagent.NewExecutor(mp, t.TempDir(), "fake-model", nil)
	result, err := executor.Run(context.Background(), subagent.AgentRequest{
		Agent:       "general-purpose",
		Mode:        "explore",
		Prompt:      "Find all Go files",
		Description: "explore codebase",
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
	if result.AgentName != "Explorer" {
		t.Errorf("expected agent name 'Explorer', got %q", result.AgentName)
	}
	if result.Content != "Explored the codebase" {
		t.Errorf("unexpected content: %q", result.Content)
	}
}

func TestAgent_UnknownAgent(t *testing.T) {
	mp := &testutil.MockProvider{}
	executor := subagent.NewExecutor(mp, t.TempDir(), "fake-model", nil)

	_, err := executor.Run(context.Background(), subagent.AgentRequest{
		Agent:  "NonExistent",
		Prompt: "do something",
	})
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

func TestAgent_MaxStepsRespected(t *testing.T) {
	// LLM always returns tool calls to force hitting max steps
	responses := make([]llm.CompletionResponse, 105)
	for i := range responses {
		responses[i] = llm.CompletionResponse{
			StopReason: "tool_use",
			ToolCalls:  []core.ToolCall{{ID: "tc", Name: "UnknownTool", Input: "{}"}},
			Usage:      llm.Usage{InputTokens: 1, OutputTokens: 1},
		}
	}

	executor := subagent.NewExecutor(
		&testutil.MockProvider{Responses: responses},
		t.TempDir(), "fake-model", nil,
	)
	result, err := executor.Run(context.Background(), subagent.AgentRequest{
		Agent:  "general-purpose",
		Prompt: "keep going",
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if result.Success {
		t.Error("expected failure (max steps)")
	}
	if !strings.Contains(result.Error, "100") {
		t.Errorf("expected error message about 100 max steps, got %q", result.Error)
	}
}

func TestAgent_ModelResolution(t *testing.T) {
	tests := []struct {
		name        string
		reqModel    string
		parentModel string
	}{
		{"request override", "custom-model", "parent-model"},
		{"parent inherited", "", "parent-model"},
		{"fallback", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mp := &testutil.MockProvider{
				Responses: []llm.CompletionResponse{
					{Content: "ok", StopReason: "end_turn"},
				},
			}
			executor := subagent.NewExecutor(mp, t.TempDir(), tt.parentModel, nil)

			if tt.parentModel != "" && executor.GetParentModelID() != tt.parentModel {
				t.Errorf("parent model mismatch: got %q, want %q",
					executor.GetParentModelID(), tt.parentModel)
			}

			_, err := executor.Run(context.Background(), subagent.AgentRequest{
				Agent:  "general-purpose",
				Prompt: "test",
				Model:  tt.reqModel,
			})
			if err != nil {
				t.Fatalf("Run() error: %v", err)
			}
		})
	}
}

// TestAgent_ExploreMode_BlocksWrites verifies that an agent configured
// with mode=explore cannot execute write tools.
// Explore mode uses a read-only permission checker, so it
// should reject Write/Edit calls.
func TestAgent_ExploreMode_BlocksWrites(t *testing.T) {
	checker := perm.ReadOnly()

	writeTools := []string{"Write", "Edit", "NotebookEdit", "Bash"}
	for _, tool := range writeTools {
		decision := checker.Check(tool, nil)
		if decision != perm.Reject {
			t.Errorf("tool %q: expected Reject in explore mode, got %v", tool, decision)
		}
	}

	readTools := []string{"Read", "Glob", "Grep", "WebFetch", "WebSearch"}
	for _, tool := range readTools {
		decision := checker.Check(tool, nil)
		if decision != perm.Permit {
			t.Errorf("tool %q: expected Permit in explore mode, got %v", tool, decision)
		}
	}

	// Also verify at the executor level: run an explore-mode agent with a Write tool
	// call queued. The tool call should be rejected (not executed), and the agent
	// should still complete because the LLM gets the error result and ends turn.
	mp := &testutil.MockProvider{
		Responses: []llm.CompletionResponse{
			// First response: LLM tries to write a file
			{
				StopReason: "tool_use",
				ToolCalls: []core.ToolCall{
					{ID: "tc1", Name: "Write", Input: `{"file_path":"/tmp/x.txt","content":"hello"}`},
				},
				Usage: llm.Usage{InputTokens: 20, OutputTokens: 10},
			},
			// Second response: LLM acknowledges the error and ends
			{
				Content:    "Cannot write files in explore mode",
				StopReason: "end_turn",
				Usage:      llm.Usage{InputTokens: 30, OutputTokens: 15},
			},
		},
	}

	executor := subagent.NewExecutor(mp, t.TempDir(), "fake-model", nil)
	result, err := executor.Run(context.Background(), subagent.AgentRequest{
		Agent:  "general-purpose",
		Mode:   "explore",
		Prompt: "try to write a file",
	})
	if err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success (agent ended turn after rejection), got error: %s", result.Error)
	}

	// The result content should come from the second response
	if !strings.Contains(result.Content, "Cannot write files in explore mode") {
		t.Errorf("unexpected final content: %q", result.Content)
	}
}

// TestAgent_SubagentHooks_Fire verifies that SubagentStart and SubagentStop
// hooks are fired when an agent runs.
func TestAgent_SubagentHooks_Fire(t *testing.T) {
	tmpDir := t.TempDir()

	// Create sentinel files that our hook scripts will touch
	startFile := filepath.Join(tmpDir, "subagent_start.txt")
	stopFile := filepath.Join(tmpDir, "subagent_stop.txt")

	// Build a settings object with SubagentStart and SubagentStop hooks.
	// Each hook writes to a temp file so we can verify it fired.
	settings := &setting.Data{
		Hooks: map[string][]setting.Hook{
			string(hook.SubagentStart): {
				{
					Matcher: "",
					Hooks: []setting.HookCmd{
						{Type: "command", Command: "touch " + startFile, Async: false},
					},
				},
			},
			string(hook.SubagentStop): {
				{
					Matcher: "",
					Hooks: []setting.HookCmd{
						{Type: "command", Command: "touch " + stopFile, Async: false},
					},
				},
			},
		},
	}

	engine := hook.NewEngine(settings, "test-session-id", tmpDir, "")

	mp := &testutil.MockProvider{
		Responses: []llm.CompletionResponse{
			{
				Content:    "done",
				StopReason: "end_turn",
				Usage:      llm.Usage{InputTokens: 10, OutputTokens: 5},
			},
		},
	}

	executor := subagent.NewExecutor(mp, tmpDir, "fake-model", engine)
	_, err := executor.Run(context.Background(), subagent.AgentRequest{
		Agent:  "general-purpose",
		Prompt: "test hooks",
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Both SubagentStart and SubagentStop are fired via ExecuteAsync (goroutines),
	// so poll briefly for each sentinel file to appear.
	waitForFile := func(path, label string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(path); err == nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Errorf("%s hook did not fire within 3s (sentinel file missing: %s)", label, path)
	}

	waitForFile(startFile, "SubagentStart")
	waitForFile(stopFile, "SubagentStop")
}

func TestAgent_BackgroundExecution(t *testing.T) {
	task.Initialize(task.Options{})
	t.Cleanup(task.ResetDefaultTracker)

	mp := &testutil.MockProvider{
		Responses: []llm.CompletionResponse{
			{
				Content: "background result", StopReason: "end_turn",
				Usage: llm.Usage{InputTokens: 10, OutputTokens: 5},
			},
		},
	}

	executor := subagent.NewExecutor(mp, t.TempDir(), "fake-model", nil)
	agentTask, err := executor.RunBackground(subagent.AgentRequest{
		Agent:       "general-purpose",
		Prompt:      "background task",
		Description: "bg test",
	})
	if err != nil {
		t.Fatalf("RunBackground() error: %v", err)
	}
	if agentTask == nil {
		t.Fatal("expected non-nil agent task")
	}

	// Wait for completion
	<-agentTask.GetContext().Done()

	info := agentTask.GetStatus()
	if info.Type != "agent" {
		t.Errorf("expected type 'agent', got %q", string(info.Type))
	}
}

func TestAgent_OnProgressReceivesToolUpdates(t *testing.T) {
	tmpDir := t.TempDir()
	readme := filepath.Join(tmpDir, "README.md")
	if err := os.WriteFile(readme, []byte("hello from agent"), 0o644); err != nil {
		t.Fatalf("WriteFile(README): %v", err)
	}

	mp := &testutil.MockProvider{
		Responses: []llm.CompletionResponse{
			{
				StopReason: "tool_use",
				Usage:      llm.Usage{InputTokens: 10, OutputTokens: 3},
				ToolCalls: []core.ToolCall{
					{
						ID:    "tc1",
						Name:  "Read",
						Input: `{"file_path":"README.md"}`,
					},
				},
			},
			{
				Content:    "Read complete",
				StopReason: "end_turn",
				Usage:      llm.Usage{InputTokens: 20, OutputTokens: 5},
			},
		},
	}

	executor := subagent.NewExecutor(mp, tmpDir, "fake-model", nil)
	var progress []string
	result, err := executor.Run(context.Background(), subagent.AgentRequest{
		Agent:  "general-purpose",
		Mode:   "explore",
		Prompt: "inspect the readme",
		OnProgress: func(msg string) {
			progress = append(progress, msg)
		},
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	for _, want := range []string{
		"Model: fake-model",
		"Mode: Explore · max 100 steps",
		"Thinking...",
		"Read(README.md)",
		"Usage: input=30 output=8",
	} {
		if !slices.Contains(progress, want) {
			t.Fatalf("progress callback values missing %q: %#v", want, progress)
		}
		if !slices.Contains(result.Progress, want) {
			t.Fatalf("result progress values missing %q: %#v", want, result.Progress)
		}
	}
}
