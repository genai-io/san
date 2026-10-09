package input

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

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
func (okExecutor) AgentConfig(name string) (tool.AgentConfigInfo, bool) {
	return tool.AgentConfigInfo{Name: name}, true
}
func (okExecutor) ResolveAgentSelection(name string) (tool.AgentConfigInfo, any, bool) {
	return tool.AgentConfigInfo{Name: name, Source: "project"}, nil, true
}
func (okExecutor) ParentModelID() string { return "m" }

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
	c := NewSlashCommandController(SlashCommandEnv{ToolSvc: reg, TaskSvc: task.Default(), Width: 120})
	return &c
}

const savedReview = "---\nname: review\ndescription: check the diff\n---\n```mermaid\nflowchart LR\n  a\n```\n\n## a\nReview {{input.base}}\n"

func TestWorkflowCommandListsSavedWorkflows(t *testing.T) {
	c := workflowController(t, map[string]string{"review": savedReview})
	notice, _, err := c.handleWorkflowCommand(context.Background(), "list")
	if err != nil || !strings.Contains(notice, "- review — check the diff") {
		t.Fatalf("notice = %q, err = %v", notice, err)
	}
	if !strings.Contains(notice, "/workflow show <name>") || !strings.Contains(notice, "/workflow run <name>") {
		t.Fatalf("list did not show organized commands: %q", notice)
	}

	empty := workflowController(t, nil)
	notice, _, _ = empty.handleWorkflowCommand(context.Background(), "")
	if !strings.Contains(notice, "No saved workflows") {
		t.Fatalf("empty notice = %q", notice)
	}
}

func TestWorkflowCommandRunsWithoutAModelTurn(t *testing.T) {
	c := workflowController(t, map[string]string{"review": savedReview})
	notice, cmd, err := c.handleWorkflowCommand(context.Background(), "run review base=main")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	// No follow-up command: nothing is submitted to the model.
	if cmd != nil {
		t.Fatal("/workflow must launch the run itself, not hand a prompt to the model")
	}
	if !strings.Contains(notice, "Started workflow review") || !strings.Contains(notice, "Task ID:") {
		t.Fatalf("notice = %q; want the launch and task ID", notice)
	}
	if strings.Contains(notice, "╭") {
		t.Fatalf("launch duplicated the live graph in its notice:\n%s", notice)
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

func TestWorkflowCommandPreviewsWithoutRunning(t *testing.T) {
	c := workflowController(t, map[string]string{"review": savedReview})
	before := len(task.Default().List())
	notice, cmd, err := c.handleWorkflowCommand(context.Background(), "show review")
	if err != nil || cmd != nil {
		t.Fatalf("preview command = %v, err = %v", cmd, err)
	}
	if !strings.Contains(notice, "Workflow review · 1 step") || !strings.Contains(notice, "  a\n") {
		t.Fatalf("preview lacks the graph:\n%s", notice)
	}
	if strings.Contains(notice, "○ pending") {
		t.Fatalf("preview contains run status:\n%s", notice)
	}
	for _, want := range []string{"Preview only", "Steps:", "a — Review {{input.base}}", "Inputs: base", "Run: /workflow run review base=<value>"} {
		if !strings.Contains(notice, want) {
			t.Errorf("preview missing %q:\n%s", want, notice)
		}
	}
	if after := len(task.Default().List()); after != before {
		t.Fatalf("preview created a task: before %d, after %d", before, after)
	}
}

func TestWorkflowPreviewCommandRunsQuotedName(t *testing.T) {
	body := strings.Replace(savedReview, "name: review", "name: release review", 1)
	c := workflowController(t, map[string]string{"release": body})
	preview, _, err := c.handleWorkflowCommand(context.Background(), `show "release review"`)
	if err != nil {
		t.Fatal(err)
	}
	_, run, ok := strings.Cut(preview, "Run: /workflow ")
	if !ok || run != `run "release review" base=<value>` {
		t.Fatalf("preview run command = %q", run)
	}
	run = strings.Replace(run, "base=<value>", `base="main branch"`, 1)
	notice, _, err := c.handleWorkflowCommand(context.Background(), run)
	if err != nil {
		t.Fatalf("generated command %q failed: %v", run, err)
	}
	_, rest, _ := strings.Cut(notice, "Task ID: ")
	id, _, _ := strings.Cut(rest, "\n")
	bg, ok := task.Default().Get(id)
	if !ok || !bg.WaitForCompletion(5*time.Second) || bg.GetStatus().Status != task.StatusCompleted {
		t.Fatalf("generated command did not complete: task=%q", id)
	}
}

func TestWorkflowCommandStartsLiveRepaint(t *testing.T) {
	c := workflowController(t, map[string]string{"review": savedReview})
	c.env.SpinnerTickCmd = func() tea.Cmd {
		return func() tea.Msg { return "workflow-tick" }
	}
	notice, cmd, err := c.handleWorkflowCommand(context.Background(), "review base=main")
	if err != nil || cmd == nil {
		t.Fatalf("launch should schedule a repaint: cmd=%v, err=%v", cmd, err)
	}
	if got := cmd(); got != "workflow-tick" {
		t.Fatalf("launch scheduled %v, want the frame clock", got)
	}
	_, rest, _ := strings.Cut(notice, "Task ID: ")
	id, _, _ := strings.Cut(rest, "\n")
	bg, ok := task.Default().Get(id)
	if !ok || !bg.WaitForCompletion(5*time.Second) {
		t.Fatalf("workflow task %q did not finish", id)
	}
}

func TestWorkflowCommandRefusesBadInput(t *testing.T) {
	c := workflowController(t, map[string]string{"review": savedReview})
	cases := map[string]string{
		"review base":  `input "base" is not key=value`,
		"nope":         "no workflow named nope",
		"review =main": `input "=main" is not key=value`,
		"run":          "usage: /workflow run <name>",
		"list extra":   "usage: /workflow list",
		"run review":   "needs input base",
	}
	for args, want := range cases {
		if _, _, err := c.handleWorkflowCommand(context.Background(), args); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("/workflow %s: err = %v, want it to mention %q", args, err, want)
		}
	}
}

func TestWorkflowStopTargetsOnlyWorkflowTasks(t *testing.T) {
	mgr := task.NewManager()
	c := NewSlashCommandController(SlashCommandEnv{TaskSvc: mgr})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wf := mgr.CreateAgentTask("workflow-test", "workflow", "/workflow run test", ctx, cancel)
	otherCtx, otherCancel := context.WithCancel(context.Background())
	defer otherCancel()
	other := mgr.CreateAgentTask("other-test", "Explore", "some agent", otherCtx, otherCancel)

	if _, _, err := c.handleWorkflowCommand(context.Background(), "stop other-test"); err == nil {
		t.Fatal("non-workflow task was accepted")
	}
	if !other.IsRunning() {
		t.Fatal("non-workflow task was stopped")
	}
	notice, _, err := c.handleWorkflowCommand(context.Background(), "stop")
	if err != nil || !strings.Contains(notice, "Stopping workflow workflow-test") {
		t.Fatalf("stop notice = %q, err = %v", notice, err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("workflow context was not cancelled")
	}
	wf.Complete(ctx.Err())
	if got := wf.GetStatus().Status; got != task.StatusStopped {
		t.Fatalf("task status = %s", got)
	}
	other.Stop()
	other.Complete(otherCtx.Err())
}
