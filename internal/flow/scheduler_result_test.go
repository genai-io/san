package flow

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
)

type toleratedPartialExecutor struct{ scriptedExecutor }

func (e *toleratedPartialExecutor) Run(ctx context.Context, req tool.AgentExecRequest) (*tool.AgentExecResult, error) {
	if req.Description == "first" {
		return &tool.AgentExecResult{Content: "unfinished supporting evidence", Error: "provider failed", AgentID: "partial-child"}, nil
	}
	return e.scriptedExecutor.Run(ctx, req)
}

func TestFlowSuccessfulResultReplacesToleratedPartialPreview(t *testing.T) {
	task.Initialize(task.Options{OutputDir: t.TempDir()})
	t.Cleanup(task.ResetDefault)
	scheduler := NewScheduler(task.Default())
	scheduler.SetExecutor(&toleratedPartialExecutor{scriptedExecutor: scriptedExecutor{outputs: map[string]string{"report": "Final conclusion\n\nComplete report"}}})
	info := runToCompletion(t, scheduler, map[string]any{"definition": "```mermaid\nflowchart LR\n first --> report\n```\n## first\ncontinue_on_error: true\n\nReview\n## report\nConclude {{first}}\n"})
	if info.Status != task.StatusCompleted || info.Output != "Final conclusion\n\nComplete report" {
		t.Fatalf("final result contaminated by partials: status=%s output=%q error=%s", info.Status, info.Output, info.Error)
	}
	log, err := os.ReadFile(info.OutputFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"unfinished supporting evidence", "partial-child", "Final conclusion"} {
		if !strings.Contains(string(log), want) {
			t.Fatalf("task log lost %q", want)
		}
	}
}
