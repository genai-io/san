package input

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	toolflow "github.com/genai-io/san/internal/tool/flow"
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

// flowController wires /flow to a Flow tool reading dir, holding
// the given saved definitions.
func flowController(t *testing.T, saved map[string]string) *SlashCommandController {
	t.Helper()
	dir := t.TempDir()
	for name, body := range saved {
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	wt := toolflow.NewFlowTool()
	wt.SetExecutor(okExecutor{})
	wt.SetSearchPaths([]string{dir})
	reg := tool.NewRegistry()
	reg.Register(wt)
	c := NewSlashCommandController(SlashCommandEnv{ToolSvc: reg, TaskSvc: task.Default(), Width: 120})
	return &c
}

const savedReview = "---\nname: review\ndescription: check the diff\n---\n```mermaid\nflowchart LR\n  a\n```\n\n## a\nReview {{input.base}}\n"

func TestFlowCanonicalCommandAndHiddenCompatibilityAlias(t *testing.T) {
	catalog := (&command.Registry{}).BuiltinNames()
	if _, ok := catalog["flow"]; !ok {
		t.Fatal("canonical Flow command is missing from the catalog")
	}
	if _, ok := catalog["workflow"]; ok {
		t.Fatal("compatibility alias must stay hidden")
	}
	for _, name := range []string{"flow", "workflow"} {
		t.Run(name, func(t *testing.T) {
			c := flowController(t, nil)
			c.env.Input, c.env.Width, c.env.Height = &Model{}, 80, 24
			notice, _, handled := c.Execute(context.Background(), "/"+name+" new example")
			v := &c.env.Input.Flow
			if !handled || notice != "" || !v.IsActive() || v.plan == nil || v.plan.Name != "example" || v.saved {
				t.Fatalf("draft dispatch: handled=%v notice=%q view=%+v", handled, notice, v)
			}
			if len(v.service.SavedDefinitions()) != 0 {
				t.Fatal("draft command saved a definition before review")
			}
		})
	}
}

func TestFlowStopBypassAcceptsCanonicalAndCompatibilityCommands(t *testing.T) {
	for input, want := range map[string]bool{
		"/flow stop": true, "/flow stop run-id": true,
		"/workflow stop": true, "/workflow stop run-id": true,
		"/flow new": false, "/workflow run review": false,
		"/flow stopper": false, "/other stop": false,
	} {
		if got := IsFlowStopCommand(input); got != want {
			t.Errorf("IsFlowStopCommand(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestFlowCommandListsSavedFlows(t *testing.T) {
	c := flowController(t, map[string]string{"review": savedReview})
	notice, _, err := c.handleFlowCommand(context.Background(), "list")
	if err != nil || !strings.Contains(notice, "- review — check the diff") {
		t.Fatalf("notice = %q, err = %v", notice, err)
	}
	if !strings.Contains(notice, "/flow show <name>") || !strings.Contains(notice, "/flow run <name>") {
		t.Fatalf("list did not show organized commands: %q", notice)
	}

	empty := flowController(t, nil)
	notice, _, _ = empty.handleFlowCommand(context.Background(), "")
	if !strings.Contains(notice, "No saved flows") {
		t.Fatalf("empty notice = %q", notice)
	}
}

func TestFlowCommandRunsWithoutAModelTurn(t *testing.T) {
	c := flowController(t, map[string]string{"review": savedReview})
	notice, cmd, err := c.handleFlowCommand(context.Background(), "run review base=main")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	// No follow-up command: nothing is submitted to the model.
	if cmd != nil {
		t.Fatal("/flow must launch the run itself, not hand a prompt to the model")
	}
	if !strings.Contains(notice, "Started flow review") || !strings.Contains(notice, "Task ID:") {
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
	// the result of the user's /flow rather than an unexplained run.
	if got := bg.GetDescription(); got != "/flow review" {
		t.Fatalf("task description = %q", got)
	}
	if info := bg.GetStatus(); info.Status != task.StatusCompleted {
		t.Fatalf("status = %s (%s)", info.Status, info.Error)
	}
}

func TestFlowCommandPreviewsWithoutRunning(t *testing.T) {
	c := flowController(t, map[string]string{"review": savedReview})
	before := len(task.Default().List())
	notice, cmd, err := c.handleFlowCommand(context.Background(), "show review")
	if err != nil || cmd != nil {
		t.Fatalf("preview command = %v, err = %v", cmd, err)
	}
	if !strings.Contains(notice, "Flow review · 1 step") || !strings.Contains(notice, "  a\n") {
		t.Fatalf("preview lacks the graph:\n%s", notice)
	}
	if strings.Contains(notice, "○ pending") {
		t.Fatalf("preview contains run status:\n%s", notice)
	}
	for _, want := range []string{"Preview only", "Steps:", "a — Review {{input.base}}", "Inputs: base", "Run: /flow run review base=<value>"} {
		if !strings.Contains(notice, want) {
			t.Errorf("preview missing %q:\n%s", want, notice)
		}
	}
	if after := len(task.Default().List()); after != before {
		t.Fatalf("preview created a task: before %d, after %d", before, after)
	}
}

func TestFlowPreviewCommandRunsQuotedName(t *testing.T) {
	body := strings.Replace(savedReview, "name: review", "name: release review", 1)
	c := flowController(t, map[string]string{"release": body})
	preview, _, err := c.handleFlowCommand(context.Background(), `show "release review"`)
	if err != nil {
		t.Fatal(err)
	}
	_, run, ok := strings.Cut(preview, "Run: /flow ")
	if !ok || run != `run "release review" base=<value>` {
		t.Fatalf("preview run command = %q", run)
	}
	run = strings.Replace(run, "base=<value>", `base="main branch"`, 1)
	notice, _, err := c.handleFlowCommand(context.Background(), run)
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

func TestFlowCommandStartsLiveRepaint(t *testing.T) {
	c := flowController(t, map[string]string{"review": savedReview})
	c.env.SpinnerTickCmd = func() tea.Cmd {
		return func() tea.Msg { return "flow-tick" }
	}
	notice, cmd, err := c.handleFlowCommand(context.Background(), "review base=main")
	if err != nil || cmd == nil {
		t.Fatalf("launch should schedule a repaint: cmd=%v, err=%v", cmd, err)
	}
	if got := cmd(); got != "flow-tick" {
		t.Fatalf("launch scheduled %v, want the frame clock", got)
	}
	_, rest, _ := strings.Cut(notice, "Task ID: ")
	id, _, _ := strings.Cut(rest, "\n")
	bg, ok := task.Default().Get(id)
	if !ok || !bg.WaitForCompletion(5*time.Second) {
		t.Fatalf("flow task %q did not finish", id)
	}
}

func TestFlowCommandRefusesBadInput(t *testing.T) {
	c := flowController(t, map[string]string{"review": savedReview})
	cases := map[string]string{
		"review base":  `input "base" is not key=value`,
		"nope":         "no flow named nope",
		"review =main": `input "=main" is not key=value`,
		"run":          "usage: /flow run <name>",
		"list extra":   "usage: /flow list",
		"run review":   "needs input base",
	}
	for args, want := range cases {
		if _, _, err := c.handleFlowCommand(context.Background(), args); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("/flow %s: err = %v, want it to mention %q", args, err, want)
		}
	}
}

func TestFlowCommandOpensManagementWithoutAModelTurn(t *testing.T) {
	c := flowController(t, map[string]string{"review": savedReview})
	c.env.Input = &Model{}
	c.env.Width, c.env.Height = 80, 24
	if notice, _, err := c.handleFlowCommand(context.Background(), ""); err != nil || notice != "" {
		t.Fatalf("open hub: notice=%q err=%v", notice, err)
	}
	if !c.env.Input.Flow.IsActive() || c.env.Input.Flow.page != "hub" {
		t.Fatal("/flow did not open its management view")
	}
	if _, _, err := c.handleFlowCommand(context.Background(), "new audit"); err != nil {
		t.Fatal(err)
	}
	v := &c.env.Input.Flow
	if v.page != "review" || v.saved || v.plan == nil || v.plan.Name != "audit" {
		t.Fatal("new flow did not enter review as an unsaved valid draft")
	}
	if len(v.plan.Nodes) != 1 || v.plan.Nodes[0].ID != "work" || !strings.Contains(v.source, "{{input}}") || strings.Contains(v.source, "## review") {
		t.Fatal("new flow should not invent a review stage")
	}
	if len(v.service.SavedDefinitions()) != 1 {
		t.Fatal("creating a draft wrote a runnable definition before review")
	}
	if _, _, err := c.handleFlowCommand(context.Background(), "edit review"); err != nil {
		t.Fatal(err)
	}
	if !c.env.Input.Flow.saved || c.env.Input.Flow.source != savedReview {
		t.Fatal("edit did not load the current saved definition")
	}
}

func TestFlowCommandFromSkillReferencesOriginalWithoutChangingIt(t *testing.T) {
	c := flowController(t, nil)
	c.env.Input = &Model{}
	c.env.Width, c.env.Height = 80, 24
	sk := &skill.Skill{Name: "audit", Namespace: "team", State: skill.StateEnable, SkillDir: "/skills/audit", AllowedTools: []string{"Read"}}
	c.env.Skill = skill.NewRegistryForTest(map[string]*skill.Skill{"team:audit": sk}, nil, nil)
	if _, _, err := c.handleFlowCommand(context.Background(), "from-skill team:audit"); err != nil {
		t.Fatal(err)
	}
	v := &c.env.Input.Flow
	if v.plan == nil || len(v.plan.Nodes) != 1 || v.plan.Name != "team-audit" || v.saved {
		t.Fatal("skill conversion should produce one reviewable draft, not guess a graph")
	}
	if !strings.Contains(v.source, `"team:audit"`) || !strings.Contains(v.source, "live skill") {
		t.Fatalf("skill dependency is not explicit:\n%s", v.source)
	}
	if sk.State != skill.StateEnable || sk.SkillDir != "/skills/audit" || len(v.service.SavedDefinitions()) != 0 {
		t.Fatal("conversion changed the skill or saved a flow without review")
	}
	sk.State = skill.StateDisable
	if _, _, err := c.handleFlowCommand(context.Background(), "from-skill team:audit"); err == nil {
		t.Fatal("disabled skill was converted")
	}
}

func TestFlowStopTargetsOnlyFlowTasks(t *testing.T) {
	mgr := task.NewManager()
	c := NewSlashCommandController(SlashCommandEnv{TaskSvc: mgr})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wf := mgr.CreateAgentTask("flow-test", "flow", "/flow run test", ctx, cancel)
	otherCtx, otherCancel := context.WithCancel(context.Background())
	defer otherCancel()
	other := mgr.CreateAgentTask("other-test", "Explore", "some agent", otherCtx, otherCancel)

	if _, _, err := c.handleFlowCommand(context.Background(), "stop other-test"); err == nil {
		t.Fatal("non-flow task was accepted")
	}
	if !other.IsRunning() {
		t.Fatal("non-flow task was stopped")
	}
	notice, _, err := c.handleFlowCommand(context.Background(), "stop")
	if err != nil || !strings.Contains(notice, "Stopping flow flow-test") {
		t.Fatalf("stop notice = %q, err = %v", notice, err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("flow context was not cancelled")
	}
	wf.Complete(ctx.Err())
	if got := wf.GetStatus().Status; got != task.StatusStopped {
		t.Fatalf("task status = %s", got)
	}
	other.Stop()
	other.Complete(otherCtx.Err())
}
