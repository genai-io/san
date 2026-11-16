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
	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/task"
)

// FlowService is the UI's project-scoped flow boundary.
type FlowService interface {
	Launch(name string, inputs map[string]string, width int) (bounds, taskID string, err error)
	Runs() ([]flow.RunRecord, error)
	InspectRun(id string) (*flow.RunRecord, error)
	ResumeRun(id, from string) (bounds, taskID string, err error)
	StopRun(id string) error
	SavedDefinitions() []flow.Saved
	Definition(name string) (string, error)
	SaveDefinition(name, definition, baseline string) error
	Saved() string
	Preview(name string, width int) (string, error)
}

type flowTick struct{ generation uint64 }

// FlowView owns command entrypoints, navigation and interaction.
// Rendering lives in on_flow_view.go.
type FlowView struct {
	service       FlowService
	ownerSession  string
	active        bool
	width, height int
	page          string
	confirm       string
	message       string
	editor        textarea.Model
	body          viewport.Model

	// Definition list and draft.
	definitions      []flow.Saved
	runs             []flow.RunRecord
	cursor           int
	name             string
	source, baseline string
	goal             string
	drafting         bool
	saved            bool
	keys             []string
	inputs           map[string]string
	field            int

	// Selected execution and its live polling cycle.
	plan       *flow.Blueprint
	record     *flow.RunRecord
	selected   int
	resumeFrom string
	follow     bool
	generation uint64
	polling    bool
	phase      int
}

type FlowDraftRequestMsg struct {
	Generation uint64
	Goal       string
}

type FlowStartedMsg struct{}

// FlowOpenTaskMsg carries only the selected execution identity, not output.
type FlowOpenTaskMsg struct {
	Generation uint64
	Execution  flow.Execution
}

type flowExecutionChoice struct {
	label     string
	execution flow.Execution
}

func (v *FlowView) SyncSession(sessionID string) {
	if !v.active {
		return
	}
	if v.ownerSession != "" && v.ownerSession != sessionID {
		v.close()
		return
	}
	v.ownerSession = sessionID
}

func (v *FlowView) AcceptTaskOpen(msg FlowOpenTaskMsg) bool {
	return v.active && v.generation == msg.Generation && v.page == "executions"
}

func (v *FlowView) TaskUnavailable() {
	v.message = "Execution unavailable in this session; inspection never starts or retries work."
}

func (v *FlowView) executionChoices() []flowExecutionChoice {
	if v.record == nil {
		return nil
	}
	node := v.record.Nodes[v.nodeID()]
	var choices []flowExecutionChoice
	seen := map[flow.Execution]bool{}
	addChoice := func(label string, number int, execution flow.Execution) {
		if execution.TaskID != "" && !seen[execution] {
			choices = append(choices, flowExecutionChoice{fmt.Sprintf("%s · attempt %d · %s", label, max(1, number), execution.TaskID), execution})
			seen[execution] = true
		}
	}
	var add func(string, flow.Checkpoint)
	add = func(label string, state flow.Checkpoint) {
		addChoice(label, state.Attempt, state.Execution)
		for _, a := range state.History {
			addChoice(label, a.Number, a.Execution)
			for _, worker := range a.Workers {
				add(fmt.Sprintf("Worker %d · flow attempt %d", worker.Index+1, a.Number), worker.State)
			}
		}
	}
	if len(node.Workers) == 0 {
		add(v.nodeID(), node)
	} else {
		for _, worker := range node.Workers {
			add(fmt.Sprintf("Worker %d", worker.Index+1), worker.State)
		}
		for _, a := range node.History {
			addChoice(v.nodeID(), a.Number, a.Execution)
			for _, worker := range a.Workers {
				add(fmt.Sprintf("Worker %d · flow attempt %d", worker.Index+1, a.Number), worker.State)
			}
		}
	}
	return choices
}

func (v *FlowView) handleExecutions(key string) tea.Cmd {
	choices := v.executionChoices()
	switch key {
	case "up", "k":
		v.cursor = max(0, v.cursor-1)
	case "down", "j":
		v.cursor = min(max(0, len(choices)-1), v.cursor+1)
	case "enter":
		if v.cursor < len(choices) {
			e := choices[v.cursor].execution
			if e.TaskID == "" || e.SessionID == "" {
				v.TaskUnavailable()
				return nil
			}
			msg := FlowOpenTaskMsg{v.generation, e}
			return func() tea.Msg { return msg }
		}
	}
	return nil
}

func (v *FlowView) IsActive() bool { return v.active }

func (v *FlowView) init(s FlowService, w, h int) {
	generation := v.generation + 1
	*v = FlowView{active: true, service: s, generation: generation, follow: true, inputs: map[string]string{}}
	v.editor = newChromelessTextarea()
	v.body = viewport.New()
	v.Resize(w, h)
}

func (v *FlowView) Open(s FlowService, w, h int) tea.Cmd {
	v.init(s, w, h)
	v.page = "hub"
	v.definitions = s.SavedDefinitions()
	var err error
	v.runs, err = s.Runs()
	v.setError(err)
	return nil
}

func (v *FlowView) OpenGeneration(s FlowService, goal string, w, h int) tea.Cmd {
	v.init(s, w, h)
	v.page, v.goal, v.drafting = "generate", goal, true
	return v.draftRequest()
}

func (v *FlowView) draftRequest() tea.Cmd {
	msg := FlowDraftRequestMsg{Generation: v.generation, Goal: v.goal}
	return func() tea.Msg { return msg }
}

func (v *FlowView) IsGenerating(generation uint64) bool {
	return v.active && v.page == "generate" && v.drafting && v.generation == generation
}

func (v *FlowView) DraftReady(generation uint64, source string, err error) bool {
	if !v.IsGenerating(generation) {
		return false
	}
	v.drafting = false
	if err != nil {
		v.setError(err)
		return true
	}
	name := "new-flow"
	if plan, parseErr := flow.Parse(source); parseErr == nil && plan.Name != "" {
		name = plan.Name
	}
	v.OpenDraft(v.service, name, source, "", v.width, v.height)
	return true
}

func (v *FlowView) OpenDraft(s FlowService, name, definition, baseline string, w, h int) tea.Cmd {
	v.init(s, w, h)
	v.name, v.source, v.baseline = name, definition, baseline
	v.saved = baseline != "" && baseline == definition
	v.validate()
	v.showReview()
	return nil
}

func (v *FlowView) OpenRun(s FlowService, id string, w, h int) tea.Cmd {
	v.init(s, w, h)
	v.page = "run"
	var err error
	v.record, err = s.InspectRun(id)
	v.setError(err)
	if v.record != nil {
		v.plan, _ = flow.Parse(v.record.Definition)
		v.focusActive()
	}
	return v.tick()
}

func (v *FlowView) Resize(w, h int) {
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

func (v *FlowView) showReview() {
	v.page = "review"
	v.body.SetHeight(max(1, v.height-6))
	if v.plan != nil {
		v.body.SetHeight(max(1, v.height-8-strings.Count(v.renderGraph(), "\n")))
	}
	v.body.SetContent(v.renderReview())
}

func (v *FlowView) tick() tea.Cmd {
	if !v.active || v.record == nil || v.record.Status != flow.RunRunning || v.polling {
		return nil
	}
	v.polling = true
	generation := v.generation
	return tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return flowTick{generation} })
}

func (v *FlowView) Update(msg tea.Msg) (tea.Cmd, bool) {
	if tick, ok := msg.(flowTick); ok {
		if !v.active || tick.generation != v.generation {
			return nil, true
		}
		v.polling = false
		v.phase++
		if v.record != nil && v.record.Status == flow.RunRunning {
			v.refreshRun()
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

func (v *FlowView) HandlePaste(s string) tea.Cmd {
	if v.confirm == "" && (v.page == "edit" || v.page == "inputs") {
		v.editor.InsertString(s)
	}
	return nil
}

func (v *FlowView) HandleKeypress(msg tea.KeyMsg) tea.Cmd {
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
	case "generate":
		if key == "enter" && !v.drafting {
			v.generation++
			v.drafting = true
			return v.draftRequest()
		}
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
	case "executions":
		return v.handleExecutions(key)
	case "details":
		v.scroll(key)
	}
	if v.page == "edit" || v.page == "inputs" {
		var cmd tea.Cmd
		v.editor, cmd = v.editor.Update(msg)
		return cmd
	}
	return nil
}

func (v *FlowView) handleHub(key string) tea.Cmd {
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

func (v *FlowView) handleReview(key string) tea.Cmd {
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
			v.body.SetHeight(max(1, v.height-6))
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

func (v *FlowView) previewSave() {
	if v.validate() {
		v.confirm = "save"
		v.body.SetHeight(max(1, v.height-9))
		v.body.SetContent(v.diffPreview())
		v.body.GotoTop()
	}
}

func (v *FlowView) submitInput() tea.Cmd {
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
	_, _, err = v.service.Launch(v.name, v.inputs, v.width)
	v.setError(err)
	if err != nil {
		return nil
	}
	v.close()
	return func() tea.Msg { return FlowStartedMsg{} }
}

func (v *FlowView) handleRun(key string) tea.Cmd {
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
		v.follow = false
		choices := v.executionChoices()
		if len(choices) == 0 {
			v.TaskUnavailable()
			return nil
		}
		v.page, v.cursor = "executions", 0
		if len(choices) == 1 {
			return v.handleExecutions("enter")
		}
	case "s":
		if v.record != nil && v.record.Status == flow.RunRunning {
			v.confirm = "stop"
		}
	case "r", "R":
		if v.record == nil || v.record.Status == flow.RunRunning {
			break
		}
		if key == "r" && v.record.Status == flow.RunSucceeded {
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

func (v *FlowView) handleConfirm(key string) tea.Cmd {
	var started tea.Cmd
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
			if err == nil {
				started = func() tea.Msg { return FlowStartedMsg{} }
			}
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
	return tea.Batch(v.tick(), started)
}

func (v *FlowView) back() {
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
	case "executions":
		v.page = "run"
	case "inputs", "details":
		v.showReview()
	default:
		v.close()
	}
}

func (v *FlowView) close() {
	v.active = false
	v.generation++
}

func (v *FlowView) setError(err error) {
	if err != nil {
		v.message = err.Error()
	}
}

func (v *FlowView) validate() bool {
	var err error
	v.plan, err = flow.Parse(v.source)
	if err == nil {
		v.selected = max(0, min(v.selected, len(v.plan.Nodes)-1))
		if v.baseline == "" && v.plan.Name != "" {
			v.name = v.plan.Name
		}
	}
	v.message = ""
	v.setError(err)
	return err == nil
}

func (v *FlowView) refreshRun() bool {
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

func (v *FlowView) focusActive() {
	if !v.follow || v.plan == nil || v.record == nil {
		return
	}
	if v.record.Status != flow.RunSucceeded {
		for i, n := range v.plan.Nodes {
			status := v.record.Nodes[n.ID].Status
			if status == flow.StatusRunning || (status == flow.StatusFailed && v.record.Status != flow.RunRunning) {
				v.selected = i
				return
			}
		}
	}
	if v.record.Status != flow.RunRunning {
		for i, n := range v.plan.Nodes {
			if len(n.Downstream()) == 0 && v.record.Nodes[n.ID].Status == flow.StatusSucceeded {
				v.selected = i
			}
		}
	}
}

func (v *FlowView) nodeID() string {
	if v.plan == nil || v.selected < 0 || v.selected >= len(v.plan.Nodes) {
		return ""
	}
	return v.plan.Nodes[v.selected].ID
}

func (v *FlowView) scroll(key string) {
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

// IsFlowStopCommand keeps cancellation out of the foreground input queue.
func IsFlowStopCommand(input string) bool {
	name, args, ok := command.ParseCommand(input)
	if !ok || name != "flow" {
		return false
	}
	fields := strings.Fields(args)
	return len(fields) > 0 && fields[0] == "stop"
}

// Authoring is loaded only for an explicit natural-language /flow request.
func (c *SlashCommandController) handleFlowCommand(_ context.Context, args string) (string, tea.Cmd, error) {
	args = strings.TrimSpace(args)
	if words := strings.Fields(args); len(words) > 0 {
		switch words[0] {
		case "list", "show", "run", "stop", "new", "from-skill", "edit", "inspect":
		default:
			if c.env.Flow == nil || c.env.Input == nil {
				return "", nil, errors.New("flow authoring is unavailable in this build")
			}
			return "", c.env.Input.Flow.OpenGeneration(c.env.Flow, args, c.env.Width, c.env.Height), nil
		}
	}
	fields, err := command.SplitArguments(args)
	if err != nil {
		return "", nil, err
	}
	if len(fields) > 0 && fields[0] == "stop" {
		return c.stopFlow(fields[1:])
	}
	svc := c.env.Flow
	if svc == nil {
		return "", nil, errors.New("flow service is unavailable in this build")
	}
	if len(fields) == 0 && c.env.Input != nil {
		return "", c.env.Input.Flow.Open(svc, c.env.Width, c.env.Height), nil
	}
	if len(fields) == 0 || fields[0] == "list" {
		if len(fields) > 1 {
			return "", nil, errors.New("usage: /flow list")
		}
		if list := svc.Saved(); list != "" {
			return "Saved flows:" + list + "\n\n" + flowUsage, nil, nil
		}
		return "No saved flows. Describe a goal with /flow <goal>.\n\n" + flowUsage, nil, nil
	}
	if fields[0] == "show" {
		if len(fields) != 2 {
			return "", nil, errors.New("usage: /flow show <name>")
		}
		preview, err := svc.Preview(fields[1], c.env.Width)
		return preview, nil, err
	}
	if fields[0] == "new" || fields[0] == "from-skill" || fields[0] == "edit" || fields[0] == "inspect" {
		if c.env.Input == nil {
			return "", nil, errors.New("flow management is unavailable in this build")
		}
		return c.openFlow(svc, fields)
	}
	if len(fields) < 2 {
		return "", nil, errors.New("usage: /flow run <name> [key=value …]")
	}
	inputs := map[string]string{}
	for _, kv := range fields[2:] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return "", nil, fmt.Errorf("input %q is not key=value", kv)
		}
		inputs[k] = v
	}
	bounds, id, err := svc.Launch(fields[1], inputs, c.env.Width)
	if err != nil {
		return "", nil, err
	}
	var cmds []tea.Cmd
	if c.env.SpinnerTickCmd != nil {
		cmds = append(cmds, c.env.SpinnerTickCmd())
	}
	notice := bounds + "\nTask ID: " + id
	notice += "\nFlow graph: task area (Alt+T) · Stop: Ctrl+C (when idle) or /flow stop " + id
	return notice, tea.Batch(cmds...), nil
}

func (c *SlashCommandController) openFlow(svc FlowService, fields []string) (string, tea.Cmd, error) {
	view := &c.env.Input.Flow
	w, h := c.env.Width, c.env.Height
	switch fields[0] {
	case "new":
		if len(fields) > 2 {
			return "", nil, errors.New("usage: /flow new [name]")
		}
		name := "new-flow"
		if len(fields) == 2 {
			name = fields[1]
		}
		src := flowDraft(name, "work", `## work
mode: explore

Complete the request below and return the result:
{{input}}
`)
		return "", view.OpenDraft(svc, name, src, "", w, h), nil
	case "from-skill":
		if len(fields) < 2 || len(fields) > 3 {
			return "", nil, errors.New("usage: /flow from-skill <skill> [name]")
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

Use the Skill tool to invoke %q for this request:
{{input}}

Preserve the skill's original order, constraints and resource paths. If it requires a parent-only tool, report that limitation rather than bypassing it.

This flow references the live skill; its instructions are loaded when the node runs. Review this before saving.
`, sk.FullName())
		return "", view.OpenDraft(svc, name, flowDraft(name, "work", prompt), "", w, h), nil
	case "edit":
		if len(fields) != 2 {
			return "", nil, errors.New("usage: /flow edit <name>")
		}
		src, err := svc.Definition(fields[1])
		if err != nil {
			return "", nil, err
		}
		return "", view.OpenDraft(svc, fields[1], src, src, w, h), nil
	case "inspect":
		if len(fields) != 2 {
			return "", nil, errors.New("usage: /flow inspect <run-id>")
		}
		return "", view.OpenRun(svc, fields[1], w, h), nil
	}
	return "", nil, errors.New("unknown flow action")
}

func flowDraft(name, graph, sections string) string {
	return fmt.Sprintf("---\nname: %q\nmax_parallel: 1\n---\n\n```mermaid\nflowchart LR\n  %s\n```\n\n%s", name, graph, sections)
}

func (c *SlashCommandController) stopFlow(args []string) (string, tea.Cmd, error) {
	if len(args) > 1 {
		return "", nil, errors.New("usage: /flow stop [task-id]")
	}
	if c.env.TaskSvc == nil {
		return "", nil, errors.New("task manager is unavailable")
	}
	var target task.BackgroundTask
	if len(args) == 1 {
		var ok bool
		target, ok = c.env.TaskSvc.Get(args[0])
		if !ok || target.GetStatus().AgentName != "flow" {
			return "", nil, fmt.Errorf("flow task %s not found", args[0])
		}
		if !target.IsRunning() {
			return "", nil, fmt.Errorf("flow task %s has already finished", args[0])
		}
	} else {
		var running []task.BackgroundTask
		for _, bg := range c.env.TaskSvc.ListRunning() {
			if bg.GetStatus().AgentName == "flow" {
				running = append(running, bg)
			}
		}
		switch len(running) {
		case 0:
			return "No running flows.", nil, nil
		case 1:
			target = running[0]
		default:
			ids := make([]string, 0, len(running))
			for _, bg := range running {
				ids = append(ids, bg.GetID())
			}
			sort.Strings(ids)
			return "Several flows are running. Choose one: /flow stop <task-id>\n  " + strings.Join(ids, "\n  "), nil, nil
		}
	}
	if err := target.Stop(); err != nil {
		return "", nil, err
	}
	return "Stopping flow " + target.GetID() + ".", nil, nil
}

const flowUsage = `Commands:
  /flow <goal>                       Generate a blueprint for review
  /flow new [name]                   Create a manual draft
  /flow from-skill <skill> [name]    Create a Skill-derived draft
  /flow edit <name>                  Review and edit a definition
  /flow list                         List saved flows
  /flow show <name>                  Preview a graph
  /flow run <name> [key=value …]     Run a flow
  /flow inspect <run-id>             Inspect or resume a run
  /flow stop [task-id]               Stop a running flow`
