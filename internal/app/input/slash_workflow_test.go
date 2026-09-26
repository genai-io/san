package input

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	toolworkflow "github.com/genai-io/san/internal/tool/workflow"
)

// okExecutor finishes every node at once, so a launched run completes.
type okExecutor struct{}

func (okExecutor) Run(context.Context, tool.AgentExecRequest) (*tool.AgentExecResult, error) {
	return &tool.AgentExecResult{Success: true, Content: "done", StepCount: 1}, nil
}
func (okExecutor) RunBackground(tool.AgentExecRequest) (tool.AgentTaskInfo, error) {
	return tool.AgentTaskInfo{}, nil
}
func (okExecutor) GetAgentConfig(name string) (tool.AgentConfigInfo, bool) {
	return tool.AgentConfigInfo{Name: name}, true
}
func (okExecutor) ResolveAgentSelection(name string) (tool.AgentConfigInfo, any, bool) {
	return tool.AgentConfigInfo{Name: name, Source: "project"}, nil, true
}
func (okExecutor) GetParentModelID() string { return "m" }

// workflowController wires /workflow to a Workflow tool reading dir, holding
// the given saved definitions.
func workflowController(t *testing.T, saved map[string]string) *SlashCommandController {
	t.Helper()
	dir := t.TempDir()
	for name, body := range saved {
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	wt := toolworkflow.NewWorkflowTool()
	wt.SetExecutor(okExecutor{})
	wt.SetSearchPaths([]string{dir})
	reg := tool.NewRegistry()
	reg.Register(wt)
	c := NewSlashCommandController(SlashCommandEnv{ToolSvc: reg})
	return &c
}

const savedReview = "---\nname: review\ndescription: check the diff\n---\n```mermaid\nflowchart LR\n  a\n```\n\n## a\nReview {{input.base}}\n"

func TestWorkflowCommandListsSavedWorkflows(t *testing.T) {
	c := workflowController(t, map[string]string{"review": savedReview})
	notice, _, err := c.handleWorkflowCommand(context.Background(), "")
	if err != nil || !strings.Contains(notice, "- review — check the diff") {
		t.Fatalf("notice = %q, err = %v", notice, err)
	}

	empty := workflowController(t, nil)
	notice, _, _ = empty.handleWorkflowCommand(context.Background(), "")
	if !strings.Contains(notice, "No saved workflows") {
		t.Fatalf("empty notice = %q", notice)
	}
}

func TestWorkflowCommandRunsWithoutAModelTurn(t *testing.T) {
	c := workflowController(t, map[string]string{"review": savedReview})
	notice, cmd, err := c.handleWorkflowCommand(context.Background(), "review base=main")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	// No follow-up command: nothing is submitted to the model.
	if cmd != nil {
		t.Fatal("/workflow must launch the run itself, not hand a prompt to the model")
	}
	if !strings.Contains(notice, "Run workflow review:") || !strings.Contains(notice, "Task ID:") {
		t.Fatalf("notice = %q; want the plan's bounds and the task", notice)
	}

	_, rest, _ := strings.Cut(notice, "Task ID: ")
	id, _, _ := strings.Cut(rest, "\n")
	bg, ok := task.Default().Get(id)
	if !ok || !bg.WaitForCompletion(5*time.Second) {
		t.Fatalf("task %q did not run to completion", id)
	}
	// The task names the command that started it, so its completion reads as
	// the result of the user's /workflow rather than an unexplained run.
	if got := bg.GetDescription(); got != "/workflow review" {
		t.Fatalf("task description = %q", got)
	}
	if info := bg.GetStatus(); info.Status != task.StatusCompleted {
		t.Fatalf("status = %s (%s)", info.Status, info.Error)
	}
}

func TestWorkflowCommandRefusesBadInput(t *testing.T) {
	c := workflowController(t, map[string]string{"review": savedReview})
	cases := map[string]string{
		"review base":  `input "base" is not key=value`,
		"nope":         "no workflow named nope",
		"review =main": `input "=main" is not key=value`,
	}
	for args, want := range cases {
		if _, _, err := c.handleWorkflowCommand(context.Background(), args); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("/workflow %s: err = %v, want it to mention %q", args, err, want)
		}
	}
}

func TestSplitQuotedKeepsQuotedValuesWhole(t *testing.T) {
	got, err := splitQuoted(`review base="main branch"  note='say "hi"' x=`)
	want := []string{"review", "base=main branch", `note=say "hi"`, "x="}
	if err != nil || strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, err %v; want %q", got, err, want)
	}
	if _, err := splitQuoted(`review base="main`); err == nil {
		t.Fatal("unclosed quote accepted")
	}
}
