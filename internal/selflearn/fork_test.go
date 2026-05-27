package selflearn

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/genai-io/gen-code/internal/core"
)

// scriptedLLM returns a queued sequence of InferResponses, one per Infer call,
// and records the last request so tests can assert on the assembled prompt.
type scriptedLLM struct {
	mu        sync.Mutex
	responses []core.InferResponse
	lastReq   core.InferRequest
	calls     int
}

func (s *scriptedLLM) InputLimit() int { return 1 << 20 }

func (s *scriptedLLM) Infer(_ context.Context, req core.InferRequest) (<-chan core.Chunk, error) {
	s.mu.Lock()
	s.lastReq = req
	s.calls++
	var r core.InferResponse
	if len(s.responses) > 0 {
		r = s.responses[0]
		s.responses = s.responses[1:]
	} else {
		r = core.InferResponse{Content: "Nothing to save.", StopReason: core.StopEndTurn}
	}
	s.mu.Unlock()

	ch := make(chan core.Chunk, 1)
	rr := r
	ch <- core.Chunk{Done: true, Response: &rr}
	close(ch)
	return ch, nil
}

func TestAllowOnlyPolicy(t *testing.T) {
	store := newTestStore(t)
	tools := core.NewTools(newMemoryWriteTool(store), newSkillManageTool(NewSkillManager("/tmp")))
	policy := allowOnly(tools)

	for _, name := range []string{"memory_write", "skill_manage"} {
		if ok, _ := policy(context.Background(), name, nil); !ok {
			t.Fatalf("%s should be allowed", name)
		}
	}
	for _, name := range []string{"Bash", "Edit", "Agent", "Read"} {
		if ok, _ := policy(context.Background(), name, nil); ok {
			t.Fatalf("%s must be denied for the reviewer", name)
		}
	}
}

func TestBuildReviewPromptSelectsArms(t *testing.T) {
	store := newTestStore(t)
	mustAdd(t, store, "existing fact about the build")
	mgr := NewSkillManager("/work/project-x")

	memOnly := buildReviewPrompt(KindMemory, "/work/project-x", mgr)
	if !strings.Contains(memOnly, "existing fact about the build") {
		t.Fatal("memory prompt should embed the current store")
	}
	if strings.Contains(memOnly, "skill_manage tool") {
		t.Fatal("memory-only prompt should not include the skill section")
	}

	skillOnly := buildReviewPrompt(KindSkills, "/work/project-x", mgr)
	if !strings.Contains(skillOnly, "skill_manage tool") {
		t.Fatal("skill prompt should include the skill section")
	}

	combined := buildReviewPrompt(KindMemory|KindSkills, "/work/project-x", mgr)
	if !strings.Contains(combined, "memory_write tool") || !strings.Contains(combined, "skill_manage tool") {
		t.Fatal("combined prompt should include both sections")
	}
}

func TestRunReviewWritesMemoryAndInheritsSystem(t *testing.T) {
	store := newTestStore(t)
	mgr := NewSkillManager("/work/project-x")

	llm := &scriptedLLM{responses: []core.InferResponse{
		{
			ToolCalls: []core.ToolCall{{
				ID:    "call-1",
				Name:  "memory_write",
				Input: `{"action":"add","content":"the user prefers tabs"}`,
			}},
			StopReason: core.StopToolUse,
		},
		{Content: "Saved 1 memory entry.", StopReason: core.StopEndTurn},
	}}

	parentSys := core.NewSystem()
	parentSys.Use(core.Section{
		Slot:   core.SlotIdentity,
		Name:   "test-identity",
		Source: core.Predefined,
		Render: func() string { return "PARENT-SYSTEM-MARKER" },
	}, "test")

	fc := ForkConfig{
		LLM:    llm,
		System: parentSys,
		CWD:    "/work/project-x",
		Memory: store,
		Skills: mgr,
	}
	snapshot := []core.Message{
		core.UserMessage("please switch the file to tabs", nil),
		core.AssistantMessage("done", "", nil),
	}

	summary, err := RunReview(context.Background(), fc, KindMemory, snapshot)
	if err != nil {
		t.Fatalf("RunReview: %v", err)
	}
	if !strings.Contains(summary, "Saved") {
		t.Fatalf("summary = %q", summary)
	}
	if got, ok := readBackMemory(t); !ok || !strings.Contains(got, "prefers tabs") {
		t.Fatalf("memory not written: got=%q ok=%v", got, ok)
	}
	if !strings.Contains(llm.lastReq.System, "PARENT-SYSTEM-MARKER") {
		t.Fatalf("fork did not inherit the parent system prompt; system=%q", llm.lastReq.System)
	}
	// The fork must only ever be offered its two write tools.
	if len(llm.lastReq.Tools) != 2 {
		t.Fatalf("fork tool count = %d, want 2", len(llm.lastReq.Tools))
	}
}

// readBackMemory reads back the store written during the test (HOME is
// already pointed at the temp dir by newTestStore).
func readBackMemory(t *testing.T) (string, bool) {
	t.Helper()
	store := NewMemoryStore("/work/project-x")
	entries := readEntries(store.Dir() + "/MEMORY.md")
	if len(entries) == 0 {
		return "", false
	}
	return strings.Join(entries, "\n"), true
}
