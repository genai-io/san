package input

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/command"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/workflow"
)

// WorkflowService is the UI's project-scoped workflow boundary.
type WorkflowService interface {
	Launch(name string, inputs map[string]string, width int) (bounds, taskID string, err error)
	Runs() ([]workflow.RunRecord, error)
	InspectRun(id string) (*workflow.RunRecord, error)
	ResumeRun(id, from string) (bounds, taskID string, err error)
	StopRun(id string) error
	SavedDefinitions() []workflow.Saved
	Definition(name string) (string, error)
	SaveDefinition(name, definition, baseline string) error
}

type workflowTick struct{ generation uint64 }

// WorkflowView owns command entrypoints, navigation and interaction.
// Rendering lives in on_workflow_view.go.
type WorkflowView struct {
	service       WorkflowService
	active        bool
	width, height int
	page          string
	confirm       string
	message       string
	editor        textarea.Model
	body          viewport.Model

	// Definition list and draft.
	definitions      []workflow.Saved
	runs             []workflow.RunRecord
	cursor           int
	name             string
	source, baseline string
	saved            bool
	keys             []string
	inputs           map[string]string
	field            int

	// Selected execution and its live polling cycle.
	plan       *workflow.Workflow
	record     *workflow.RunRecord
	selected   int
	resumeFrom string
	follow     bool
	generation uint64
	polling    bool
	phase      int
}

func (v *WorkflowView) IsActive() bool { return v.active }

func (v *WorkflowView) init(s WorkflowService, w, h int) {
	generation := v.generation + 1
	*v = WorkflowView{active: true, service: s, generation: generation, follow: true, inputs: map[string]string{}}
	v.editor = newChromelessTextarea()
	v.body = viewport.New()
	v.Resize(w, h)
}

func (v *WorkflowView) Open(s WorkflowService, w, h int) tea.Cmd {
	v.init(s, w, h)
	v.page = "hub"
	v.definitions = s.SavedDefinitions()
	var err error
	v.runs, err = s.Runs()
	v.setError(err)
	return nil
}

func (v *WorkflowView) OpenDraft(s WorkflowService, name, definition, baseline string, w, h int) tea.Cmd {
	v.init(s, w, h)
	v.name, v.source, v.baseline = name, definition, baseline
	v.saved = baseline != "" && baseline == definition
	v.validate()
	v.showReview()
	return nil
}

func (v *WorkflowView) OpenTask(s WorkflowService, taskID string, w, h int) tea.Cmd {
	runs, err := s.Runs()
	if err == nil {
		for _, r := range runs {
			if r.TaskID == taskID {
				return v.OpenRun(s, r.ID, w, h)
			}
		}
	}
	v.Open(s, w, h)
	v.message = fmt.Sprintf("Task %s launched; its run details are not available yet", taskID)
	v.setError(err)
	return nil
}

func (v *WorkflowView) OpenRun(s WorkflowService, id string, w, h int) tea.Cmd {
	v.init(s, w, h)
	v.page = "run"
	var err error
	v.record, err = s.InspectRun(id)
	v.setError(err)
	if v.record != nil {
		v.plan, _ = workflow.Parse(v.record.Definition)
		v.focusActive()
	}
	return v.tick()
}

func (v *WorkflowView) Resize(w, h int) {
	v.width, v.height = max(12, w), max(8, h)
	v.editor.SetWidth(max(1, w-4))
	v.editor.SetHeight(max(1, h-9))
	v.body.SetWidth(max(1, w-4))
	v.body.SetHeight(max(1, h-10))
	if v.page == "review" && v.confirm == "" {
		v.showReview()
	} else if v.page == "details" {
		v.body.SetHeight(max(1, v.height-6))
		v.body.SetContent(v.renderLimits())
	}
}

func (v *WorkflowView) showReview() {
	v.page = "review"
	v.body.SetHeight(max(1, v.height-6))
	v.body.SetContent(v.renderReview())
}

func (v *WorkflowView) tick() tea.Cmd {
	if !v.active || v.record == nil || v.record.Status != workflow.RunRunning || v.polling {
		return nil
	}
	v.polling = true
	generation := v.generation
	return tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return workflowTick{generation} })
}

func (v *WorkflowView) Update(msg tea.Msg) (tea.Cmd, bool) {
	if tick, ok := msg.(workflowTick); ok {
		if !v.active || tick.generation != v.generation {
			return nil, true
		}
		v.polling = false
		v.phase++
		if v.record != nil && v.record.Status == workflow.RunRunning && v.refreshRun() && v.page == "output" && v.confirm == "" {
			offset := v.body.YOffset()
			v.body.SetContent(v.inspectOutput())
			v.body.SetYOffset(offset)
		}
		return v.tick(), true
	}
	if v.active && (v.page == "edit" || v.page == "inputs") {
		var cmd tea.Cmd
		v.editor, cmd = v.editor.Update(msg)
		return cmd, true
	}
	return nil, false
}

func (v *WorkflowView) HandlePaste(s string) tea.Cmd {
	if v.confirm == "" && (v.page == "edit" || v.page == "inputs") {
		v.editor.InsertString(s)
	}
	return nil
}

func (v *WorkflowView) HandleKeypress(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	v.message = ""
	if v.confirm != "" {
		return v.handleConfirm(key)
	}
	if key == "esc" {
		v.back()
		return nil
	}
	switch v.page {
	case "hub":
		return v.handleHub(key)
	case "review":
		return v.handleReview(key)
	case "edit":
		if key == "ctrl+s" {
			v.source, v.saved = v.editor.Value(), false
			v.previewSave()
			return nil
		}
	case "inputs":
		if key == "enter" {
			return v.submitInput()
		}
	case "run":
		return v.handleRun(key)
	case "output", "details":
		v.scroll(key)
	}
	if v.page == "edit" || v.page == "inputs" {
		var cmd tea.Cmd
		v.editor, cmd = v.editor.Update(msg)
		return cmd
	}
	return nil
}

func (v *WorkflowView) handleHub(key string) tea.Cmd {
	switch key {
	case "up", "k":
		v.cursor = max(0, v.cursor-1)
	case "down", "j":
		v.cursor = min(max(0, len(v.definitions)+len(v.runs)-1), v.cursor+1)
	case "enter":
		if v.cursor < len(v.definitions) {
			name := v.definitions[v.cursor].Name
			source, err := v.service.Definition(name)
			if err != nil {
				v.setError(err)
				return nil
			}
			return v.OpenDraft(v.service, name, source, source, v.width, v.height)
		}
		if i := v.cursor - len(v.definitions); i >= 0 && i < len(v.runs) {
			return v.OpenRun(v.service, v.runs[i].ID, v.width, v.height)
		}
	}
	return nil
}

func (v *WorkflowView) handleReview(key string) tea.Cmd {
	switch key {
	case "up", "k", "down", "j":
		if v.plan != nil {
			delta := -1
			if key == "down" || key == "j" {
				delta = 1
			}
			v.selected = max(0, min(len(v.plan.Nodes)-1, v.selected+delta))
			v.showReview()
			v.body.GotoTop()
		}
	case "d":
		if v.plan != nil {
			v.page = "details"
			v.body.SetContent(v.renderLimits())
			v.body.GotoTop()
		}
	case "e":
		v.page = "edit"
		v.editor.SetValue(v.source)
		return v.editor.Focus()
	case "r":
		if v.saved && v.validate() {
			v.keys, v.field, v.page = v.plan.InputNames(), 0, "inputs"
			v.editor.SetValue("")
			v.editor.Focus()
		}
	case "s":
		v.previewSave()
	default:
		v.scroll(key)
	}
	return nil
}

func (v *WorkflowView) previewSave() {
	if v.validate() {
		v.confirm = "save"
		v.body.SetHeight(max(1, v.height-9))
		v.body.SetContent(v.diffPreview())
		v.body.GotoTop()
	}
}

func (v *WorkflowView) submitInput() tea.Cmd {
	if len(v.keys) > 0 {
		value := strings.TrimSpace(v.editor.Value())
		if value == "" {
			v.message = "A nonempty value is required"
			return nil
		}
		v.inputs[v.keys[v.field]] = value
		v.field++
		v.editor.SetValue("")
		if v.field < len(v.keys) {
			return nil
		}
		v.field = len(v.keys) - 1
		v.editor.SetValue(v.inputs[v.keys[v.field]])
	}
	current, err := v.service.Definition(v.name)
	if err != nil || current != v.source {
		v.saved = false
		v.showReview()
		v.message = "Definition changed since review; reopen it before running"
		v.setError(err)
		return nil
	}
	_, id, err := v.service.Launch(v.name, v.inputs, v.width)
	v.setError(err)
	if err != nil {
		return nil
	}
	return v.OpenTask(v.service, id, v.width, v.height)
}

func (v *WorkflowView) handleRun(key string) tea.Cmd {
	switch key {
	case "up", "k", "down", "j":
		v.follow = false
		delta := -1
		if key == "down" || key == "j" {
			delta = 1
		}
		if v.plan != nil {
			v.selected = max(0, min(len(v.plan.Nodes)-1, v.selected+delta))
		}
	case "f":
		v.follow = true
		v.focusActive()
	case "enter":
		v.follow, v.page = false, "output"
		v.body.SetContent(v.inspectOutput())
		v.body.GotoTop()
	case "s":
		if v.record != nil && v.record.Status == workflow.RunRunning {
			v.confirm = "stop"
		}
	case "r", "R":
		if v.record == nil || v.record.Status == workflow.RunRunning {
			break
		}
		if key == "r" && v.record.Status == workflow.RunSucceeded {
			v.message = "Select a node and use R to rerun it"
			break
		}
		v.resumeFrom = ""
		if key == "R" {
			v.resumeFrom = v.nodeID()
		}
		v.confirm = "resume"
		v.body.SetContent(v.resumePreview())
		v.body.GotoTop()
	}
	return nil
}

func (v *WorkflowView) handleConfirm(key string) tea.Cmd {
	switch key {
	case "y":
		switch v.confirm {
		case "save":
			if v.validate() {
				err := v.service.SaveDefinition(v.name, v.source, v.baseline)
				v.setError(err)
				if err == nil {
					v.baseline, v.saved, v.page = v.source, true, "review"
				}
			}
		case "stop":
			v.setError(v.service.StopRun(v.record.ID))
			v.refreshRun()
		case "resume":
			_, _, err := v.service.ResumeRun(v.record.ID, v.resumeFrom)
			v.setError(err)
			v.refreshRun()
		case "discard":
			v.close()
		}
		v.confirm = ""
	case "n", "esc":
		v.confirm = ""
	default:
		v.scroll(key)
	}
	if v.confirm == "" && v.page == "review" {
		v.showReview()
	}
	return v.tick()
}

func (v *WorkflowView) back() {
	switch v.page {
	case "edit":
		v.source = v.editor.Value()
		v.saved = v.baseline != "" && v.source == v.baseline
		v.validate()
		v.showReview()
	case "review":
		if !v.saved {
			v.confirm = "discard"
		} else {
			v.close()
		}
	case "output":
		v.page = "run"
	case "inputs", "details":
		v.showReview()
	default:
		v.close()
	}
}

func (v *WorkflowView) close() {
	v.active = false
	v.generation++
}

func (v *WorkflowView) setError(err error) {
	if err != nil {
		v.message = err.Error()
	}
}

func (v *WorkflowView) validate() bool {
	var err error
	v.plan, err = workflow.Parse(v.source)
	if err == nil {
		v.selected = max(0, min(v.selected, len(v.plan.Nodes)-1))
	}
	v.message = ""
	v.setError(err)
	return err == nil
}

func (v *WorkflowView) refreshRun() bool {
	if v.record == nil {
		return false
	}
	record, err := v.service.InspectRun(v.record.ID)
	v.setError(err)
	if record == nil {
		return false
	}
	v.record = record
	v.focusActive()
	return true
}

func (v *WorkflowView) focusActive() {
	if !v.follow || v.plan == nil || v.record == nil {
		return
	}
	if v.record.Status != workflow.RunSucceeded {
		for i, n := range v.plan.Nodes {
			status := v.record.Nodes[n.ID].Status
			if status == workflow.StatusRunning || (status == workflow.StatusFailed && v.record.Status != workflow.RunRunning) {
				v.selected = i
				return
			}
		}
	}
	if v.record.Status != workflow.RunRunning {
		for i, n := range v.plan.Nodes {
			if len(n.Downstream()) == 0 && v.record.Nodes[n.ID].Status == workflow.StatusSucceeded {
				v.selected = i
			}
		}
	}
}

func (v *WorkflowView) nodeID() string {
	if v.plan == nil || v.selected < 0 || v.selected >= len(v.plan.Nodes) {
		return ""
	}
	return v.plan.Nodes[v.selected].ID
}

func (v *WorkflowView) scroll(key string) {
	switch key {
	case "up", "k":
		v.body.ScrollUp(1)
	case "down", "j":
		v.body.ScrollDown(1)
	case "pgup":
		v.body.PageUp()
	case "pgdown":
		v.body.PageDown()
	case "home":
		v.body.GotoTop()
	case "end":
		v.body.GotoBottom()
	}
}

// IsWorkflowStopCommand keeps cancellation out of the foreground input queue.
func IsWorkflowStopCommand(input string) bool {
	name, args, ok := command.ParseCommand(input)
	if !ok || name != "workflow" {
		return false
	}
	fields := strings.Fields(args)
	return len(fields) > 0 && fields[0] == "stop"
}

type workflowRunner interface {
	Saved() string
	Preview(name string, width int) (string, error)
	Launch(name string, inputs map[string]string, width int) (bounds, taskID string, err error)
}

// Management is explicit and needs no model turn or ambient authoring prompt.
func (c *SlashCommandController) handleWorkflowCommand(_ context.Context, args string) (string, tea.Cmd, error) {
	fields, err := command.SplitArguments(args)
	if err != nil {
		return "", nil, err
	}
	if len(fields) > 0 && fields[0] == "stop" {
		return c.stopWorkflow(fields[1:])
	}
	if c.env.ToolSvc == nil {
		return "", nil, errors.New("the Workflow tool is not available in this build")
	}
	t, _ := c.env.ToolSvc.Get(tool.ToolWorkflow)
	wt, ok := t.(workflowRunner)
	if !ok {
		return "", nil, errors.New("the Workflow tool is not available in this build")
	}
	if len(fields) == 0 && c.env.Input != nil {
		if svc, ok := t.(WorkflowService); ok {
			return "", c.env.Input.Workflow.Open(svc, c.env.Width, c.env.Height), nil
		}
	}
	if len(fields) == 0 || fields[0] == "list" {
		if len(fields) > 1 {
			return "", nil, errors.New("usage: /workflow list")
		}
		if list := wt.Saved(); list != "" {
			return "Saved workflows:" + list + "\n\n" + workflowUsage, nil, nil
		}
		return "No saved workflows. Create one with /workflow new [name].\n\n" + workflowUsage, nil, nil
	}
	if fields[0] == "show" {
		if len(fields) != 2 {
			return "", nil, errors.New("usage: /workflow show <name>")
		}
		preview, err := wt.Preview(fields[1], c.env.Width)
		return preview, nil, err
	}
	if fields[0] == "new" || fields[0] == "from-skill" || fields[0] == "edit" || fields[0] == "inspect" {
		svc, ok := t.(WorkflowService)
		if !ok || c.env.Input == nil {
			return "", nil, errors.New("workflow management is unavailable in this build")
		}
		return c.openWorkflow(svc, fields)
	}
	if fields[0] == "run" {
		if len(fields) < 2 {
			return "", nil, errors.New("usage: /workflow run <name> [key=value …]")
		}
		fields = fields[1:]
	}
	inputs := map[string]string{}
	for _, kv := range fields[1:] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return "", nil, fmt.Errorf("input %q is not key=value", kv)
		}
		inputs[k] = v
	}
	bounds, id, err := wt.Launch(fields[0], inputs, c.env.Width)
	if err != nil {
		return "", nil, err
	}
	var cmds []tea.Cmd
	if c.env.SpinnerTickCmd != nil {
		cmds = append(cmds, c.env.SpinnerTickCmd())
	}
	if c.env.Input != nil {
		if svc, ok := t.(WorkflowService); ok {
			cmds = append(cmds, c.env.Input.Workflow.OpenTask(svc, id, c.env.Width, c.env.Height))
		}
	}
	notice := bounds + "\nTask ID: " + id
	notice += "\nLive activity: task area (Alt+T) · Stop: Ctrl+C (when idle) or /workflow stop " + id
	return notice, tea.Batch(cmds...), nil
}

func (c *SlashCommandController) openWorkflow(svc WorkflowService, fields []string) (string, tea.Cmd, error) {
	view := &c.env.Input.Workflow
	w, h := c.env.Width, c.env.Height
	switch fields[0] {
	case "new":
		if len(fields) > 2 {
			return "", nil, errors.New("usage: /workflow new [name]")
		}
		name := "new-workflow"
		if len(fields) == 2 {
			name = fields[1]
		}
		src := workflowDraft(name, "work --> review", `## work
mode: explore

Complete this task and report your findings:
{{input.task}}

## review
mode: explore

Check these findings against the task and produce the final result.
Task: {{input.task}}
Findings: {{work}}
`)
		return "", view.OpenDraft(svc, name, src, "", w, h), nil
	case "from-skill":
		if len(fields) < 2 || len(fields) > 3 {
			return "", nil, errors.New("usage: /workflow from-skill <skill> [name]")
		}
		if c.env.Skill == nil {
			return "", nil, errors.New("skill registry is unavailable")
		}
		sk, ok := c.env.Skill.Get(fields[1])
		if !ok || !sk.IsEnabled() {
			return "", nil, fmt.Errorf("skill %q is missing or disabled", fields[1])
		}
		name := strings.ReplaceAll(sk.FullName(), ":", "-")
		if len(fields) == 3 {
			name = fields[2]
		}
		// Let the Skill loader retain its instructions and resource paths;
		// guessing a graph from prose could change the original behavior.
		prompt := fmt.Sprintf(`## work
mode: default

Use the Skill tool to invoke %q for this task:
{{input.task}}

Preserve the skill's original order, constraints and resource paths. If it requires a parent-only tool, report that limitation rather than bypassing it.

This workflow references the live skill; its instructions are loaded when the node runs. Review this before saving.
`, sk.FullName())
		return "", view.OpenDraft(svc, name, workflowDraft(name, "work", prompt), "", w, h), nil
	case "edit":
		if len(fields) != 2 {
			return "", nil, errors.New("usage: /workflow edit <name>")
		}
		src, err := svc.Definition(fields[1])
		if err != nil {
			return "", nil, err
		}
		return "", view.OpenDraft(svc, fields[1], src, src, w, h), nil
	case "inspect":
		if len(fields) != 2 {
			return "", nil, errors.New("usage: /workflow inspect <run-id>")
		}
		return "", view.OpenRun(svc, fields[1], w, h), nil
	}
	return "", nil, errors.New("unknown workflow action")
}

func workflowDraft(name, graph, sections string) string {
	return fmt.Sprintf("---\nname: %q\nmax_parallel: 1\n---\n\n```mermaid\nflowchart LR\n  %s\n```\n\n%s", name, graph, sections)
}

func (c *SlashCommandController) stopWorkflow(args []string) (string, tea.Cmd, error) {
	if len(args) > 1 {
		return "", nil, errors.New("usage: /workflow stop [task-id]")
	}
	if c.env.TaskSvc == nil {
		return "", nil, errors.New("task manager is unavailable")
	}
	var target task.BackgroundTask
	if len(args) == 1 {
		var ok bool
		target, ok = c.env.TaskSvc.Get(args[0])
		if !ok || target.GetStatus().AgentName != "workflow" {
			return "", nil, fmt.Errorf("workflow task %s not found", args[0])
		}
		if !target.IsRunning() {
			return "", nil, fmt.Errorf("workflow task %s has already finished", args[0])
		}
	} else {
		var running []task.BackgroundTask
		for _, bg := range c.env.TaskSvc.ListRunning() {
			if bg.GetStatus().AgentName == "workflow" {
				running = append(running, bg)
			}
		}
		switch len(running) {
		case 0:
			return "No running workflows.", nil, nil
		case 1:
			target = running[0]
		default:
			ids := make([]string, 0, len(running))
			for _, bg := range running {
				ids = append(ids, bg.GetID())
			}
			sort.Strings(ids)
			return "Several workflows are running. Choose one: /workflow stop <task-id>\n  " + strings.Join(ids, "\n  "), nil, nil
		}
	}
	if err := target.Stop(); err != nil {
		return "", nil, err
	}
	return "Stopping workflow " + target.GetID() + ".", nil, nil
}

const workflowUsage = `Commands:
  /workflow new [name]                   Create a reviewable draft
  /workflow from-skill <skill> [name]    Create a Skill-derived draft
  /workflow edit <name>                  Review and edit a definition
  /workflow list                         List saved workflows
  /workflow show <name>                  Preview a graph
  /workflow run <name> [key=value …]     Run a workflow
  /workflow inspect <run-id>             Inspect or resume a run
  /workflow stop [task-id]               Stop a running workflow`
