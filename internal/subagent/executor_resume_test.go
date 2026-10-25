package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type continuationProvider struct{ driver *aitest.Driver }

func (p continuationProvider) Client(string, map[string]string) (*ai.Client, error) {
	return p.driver.Client(), nil
}
func (p continuationProvider) ListModels(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (p continuationProvider) Name() string                                        { return "aitest" }

func TestNativeBackgroundContinuation(t *testing.T) {
	store, err := session.NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	path := filepath.Join(cwd, "useful.txt")
	if err := os.WriteFile(path, []byte("previously discovered evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"file_path": path})
	d := aitest.New(aitest.Asks(ai.ToolCall{ID: "read-1", Name: "Read", Input: string(args)}), aitest.Asks(ai.ToolCall{ID: "read-2", Name: "Read", Input: string(args)}), aitest.Says("verified follow-up"))
	e := NewExecutor(continuationProvider{d}, cwd, "aitest", nil)
	e.SetSessionStore(store, "parent-native-test")
	a, err := e.RunBackground(tool.AgentExecRequest{Prompt: "Inspect original task", MaxSteps: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !a.WaitForCompletion(5 * time.Second) {
		t.Fatal("first execution hung")
	}
	first := a.GetStatus()
	if first.Status != task.StatusFailed || first.AgentSessionID == "" {
		t.Fatalf("first execution: %+v", first)
	}
	b, err := e.RunBackground(tool.AgentExecRequest{ResumeTaskID: a.GetID(), Prompt: "Check it now", MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("new logical task allocated")
	}
	if !b.WaitForCompletion(5 * time.Second) {
		t.Fatal("continuation hung")
	}
	second := b.GetStatus()
	if second.Status != task.StatusCompleted || second.AgentSessionID != first.AgentSessionID || len(second.Attempts) != 2 || second.Attempts[0].Error == "" {
		t.Fatalf("continuation: %+v", second)
	}
	var text strings.Builder
	for _, m := range d.Last().Messages {
		text.WriteString(m.Text())
		for _, result := range m.ToolResults() {
			text.WriteString(fmt.Sprint(result))
		}
	}
	if !strings.Contains(text.String(), "Inspect original task") || !strings.Contains(text.String(), "Check it now") || !strings.Contains(text.String(), "previously discovered evidence") {
		t.Fatalf("native history missing: %s", text.String())
	}
	h, err := store.Read(first.AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Messages) < 4 {
		t.Fatalf("native history not appended: %+v", h.Messages)
	}
}
