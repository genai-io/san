package input

import (
	"fmt"
	"maps"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/task"
)

// FlowService is the UI's project-scoped flow boundary.
type FlowService interface {
	Launch(name string, inputs map[string]string, width int) (taskID string, err error)
	Runs() ([]flow.RunRecord, error)
	GetRun(id string) (*flow.RunRecord, error)
	ResumeRun(id, from string, width int) (taskID string, err error)
	StopRun(id string) error
	SavedDefinitions() []flow.SavedDefinition
	SavedInputNames(name string) ([]string, error)
	Definition(name string) (string, error)
	SaveDefinition(name, definition, baseline string) error
	Preview(name string, width int) (string, error)
}

type flowTick struct{ generation uint64 }

// flowPage is the FlowView screen.
type flowPage string

const (
	flowPageHub        flowPage = "hub"
	flowPageGoal       flowPage = "goal"
	flowPageGenerate   flowPage = "generate"
	flowPageReview     flowPage = "review"
	flowPageDetails    flowPage = "details"
	flowPageEdit       flowPage = "edit"
	flowPageInputs     flowPage = "inputs"
	flowPageRun        flowPage = "run"
	flowPageExecutions flowPage = "executions"
)

// flowConfirm is the action awaiting confirmation; empty when none is.
type flowConfirm string

const (
	flowConfirmSave    flowConfirm = "save"
	flowConfirmStop    flowConfirm = "stop"
	flowConfirmDiscard flowConfirm = "discard"
	flowConfirmResume  flowConfirm = "resume"
)

// FlowView owns command entrypoints, navigation and interaction.
// Rendering lives in on_flow_view.go.
type FlowView struct {
	service       FlowService
	ownerSession  string
	active        bool
	width, height int
	page          flowPage
	confirm       flowConfirm
	message       string
	editor        textarea.Model
	body          viewport.Model

	// Definition list and draft.
	definitions      []flow.SavedDefinition
	runs             []flow.RunRecord
	cursor           int
	name             string
	source, baseline string
	goal             string
	draftAt          time.Time
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
	state     flow.Checkpoint
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
	if !v.active || v.generation != msg.Generation || v.page != flowPageExecutions {
		return false
	}
	choices := v.executionChoices()
	return v.cursor >= 0 && v.cursor < len(choices) && choices[v.cursor].execution == msg.Execution && msg.Execution.SessionID != ""
}

// RecordedTask derives read-only detail from the accepted checkpoint, never from
// event payloads or a session loader. Pending work is not a recorded result.
func (v *FlowView) RecordedTask(msg FlowOpenTaskMsg) (TaskViewItem, bool) {
	if !v.AcceptTaskOpen(msg) {
		return TaskViewItem{}, false
	}
	choice := v.executionChoices()[v.cursor]
	if choice.execution.MessageID != "" {
		return TaskViewItem{}, false
	}
	state := choice.state
	status := task.StatusCompleted
	switch state.Status {
	case flow.StatusSucceeded:
	case flow.StatusFailed:
		status = task.StatusFailed
	default:
		return TaskViewItem{}, false
	}
	return TaskViewItem{ID: choice.execution.TaskID, SessionID: choice.execution.SessionID,
		Subject: choice.label, Attempt: state.Attempt, Status: status,
		Output: state.Output, Error: state.Error, Recorded: true}, true
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
	addChoice := func(label string, state flow.Checkpoint) {
		execution, number := state.Execution, state.Attempt
		if (execution.TaskID != "" || execution.MessageID != "") && !seen[execution] {
			identity := execution.TaskID
			if execution.MessageID != "" {
				identity = "main message " + execution.MessageID
			}
			choices = append(choices, flowExecutionChoice{fmt.Sprintf("%s · attempt %d · %s", label, max(1, number), identity), execution, state})
			seen[execution] = true
		}
	}
	var add func(string, flow.Checkpoint)
	add = func(label string, state flow.Checkpoint) {
		addChoice(label, state)
		for _, a := range state.History {
			addChoice(label, flow.Checkpoint{Outcome: a.Outcome, Attempt: a.Number})
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
			addChoice(v.nodeID(), flow.Checkpoint{Outcome: a.Outcome, Attempt: a.Number})
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
			if (e.TaskID == "" && e.MessageID == "") || e.SessionID == "" {
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
	v.page = flowPageHub
	v.definitions = s.SavedDefinitions()
	var err error
	v.runs, err = s.Runs()
	v.setError(err)
	return nil
}

func (v *FlowView) openGoal() tea.Cmd {
	v.page = flowPageGoal
	v.draftAt = time.Time{}
	v.Resize(v.width, v.height)
	v.editor.Placeholder = "Describe the work and the result you want"
	v.editor.SetValue(v.goal)
	return v.editor.Focus()
}

func (v *FlowView) OpenGeneration(s FlowService, goal string, w, h int) tea.Cmd {
	v.init(s, w, h)
	v.page, v.goal = flowPageGenerate, goal
	v.draftAt = time.Now()
	return v.draftRequest()
}

func (v *FlowView) SetDraftFrame(frame int) {
	if v.IsGenerating(v.generation) {
		v.phase = frame
	}
}

func (v *FlowView) draftRequest() tea.Cmd {
	msg := FlowDraftRequestMsg{Generation: v.generation, Goal: v.goal}
	return func() tea.Msg { return msg }
}

func (v *FlowView) IsGenerating(generation uint64) bool {
	return v.active && v.page == flowPageGenerate && !v.draftAt.IsZero() && v.generation == generation
}

func (v *FlowView) DraftReady(generation uint64, source string, err error) bool {
	if !v.IsGenerating(generation) {
		return false
	}
	v.draftAt = time.Time{}
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
	v.page = flowPageRun
	var err error
	v.record, err = s.GetRun(id)
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
	if v.page == flowPageReview && v.confirm == "" {
		v.showReview()
	} else if v.page == flowPageDetails {
		v.body.SetHeight(max(1, v.height-6))
		v.body.SetContent(v.renderLimits())
	} else if v.page == flowPageInputs {
		v.resizeInputs()
	} else if v.page == flowPageGoal {
		v.editor.SetWidth(max(1, min(72, v.width-8)))
		v.editor.SetHeight(max(1, min(6, v.height-10)))
	}
}

func (v *FlowView) showReview() {
	v.page = flowPageReview
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
	if v.active && (v.page == flowPageEdit || v.page == flowPageGoal || (v.page == flowPageInputs && v.field < len(v.keys))) {
		var cmd tea.Cmd
		v.editor, cmd = v.editor.Update(msg)
		return cmd, true
	}
	return nil, false
}

func (v *FlowView) HandlePaste(s string) tea.Cmd {
	if v.confirm == "" && (v.page == flowPageEdit || v.page == flowPageGoal || (v.page == flowPageInputs && v.field < len(v.keys))) {
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
	case flowPageGoal:
		switch key {
		case "enter":
			goal := strings.TrimSpace(v.editor.Value())
			if goal == "" {
				v.message = "Describe a goal to continue"
				return nil
			}
			return v.OpenGeneration(v.service, goal, v.width, v.height)
		case "shift+enter", "alt+enter", "ctrl+j":
			v.editor.InsertString("\n")
			return nil
		}
	case flowPageGenerate:
		if key == "enter" && v.draftAt.IsZero() {
			v.generation++
			v.draftAt, v.phase = time.Now(), 0
			return v.draftRequest()
		}
		if key == "e" && v.draftAt.IsZero() {
			return v.openGoal()
		}
	case flowPageHub:
		return v.handleHub(key)
	case flowPageReview:
		return v.handleReview(key)
	case flowPageEdit:
		if key == "ctrl+s" {
			v.source, v.saved = v.editor.Value(), false
			v.previewSave()
			return nil
		}
	case flowPageInputs:
		switch key {
		case "enter", "tab":
			if v.field < len(v.keys) {
				if strings.TrimSpace(v.editor.Value()) == "" {
					v.message = "Enter a value to continue"
					return nil
				}
				v.inputs[v.keys[v.field]] = strings.TrimSpace(v.editor.Value())
				return v.openInputs()
			}
			if key == "enter" {
				return v.submitInput()
			}
		case "shift+tab":
			if len(v.keys) > 0 {
				v.keepInput()
				v.field = max(0, v.field-1)
				return v.showInput()
			}
		case "shift+enter":
			if v.field < len(v.keys) {
				v.editor.InsertString("\n")
			}
			return nil
		default:
			if v.field == len(v.keys) {
				v.scroll(key)
			}
		}
	case flowPageRun:
		return v.handleRun(key)
	case flowPageExecutions:
		return v.handleExecutions(key)
	case flowPageDetails:
		v.scroll(key)
	}
	if v.page == flowPageEdit || v.page == flowPageGoal || (v.page == flowPageInputs && v.field < len(v.keys)) {
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
		v.cursor = min(len(v.definitions)+len(v.runs), v.cursor+1)
	case "n":
		return v.openGoal()
	case "enter":
		if v.cursor == 0 {
			return v.openGoal()
		}
		if v.cursor <= len(v.definitions) {
			name := v.definitions[v.cursor-1].Name
			source, err := v.service.Definition(name)
			if err != nil {
				v.setError(err)
				return nil
			}
			return v.OpenDraft(v.service, name, source, source, v.width, v.height)
		}
		if i := v.cursor - len(v.definitions) - 1; i >= 0 && i < len(v.runs) {
			return v.OpenRun(v.service, v.runs[i].ID, v.width, v.height)
		}
	}
	return nil
}

func (v *FlowView) handleReview(key string) tea.Cmd {
	if key == "enter" {
		key = "s"
		if v.plan == nil {
			key = "e"
		} else if v.saved {
			key = "r"
		}
	}
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
			v.page = flowPageDetails
			v.body.SetHeight(max(1, v.height-6))
			v.body.SetContent(v.renderLimits())
			v.body.GotoTop()
		}
	case "e":
		v.page = flowPageEdit
		v.Resize(v.width, v.height)
		v.editor.Placeholder = ""
		v.editor.SetValue(v.source)
		return v.editor.Focus()
	case "r":
		if v.saved && v.validate() {
			return v.openInputs()
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
		v.confirm = flowConfirmSave
		v.body.SetHeight(max(1, v.height-9))
		v.body.SetContent(v.diffPreview())
		v.body.GotoTop()
	}
}

// OpenRunInputs opens the run form for a saved definition, keeping the
// inputs already supplied on the command line.
func (v *FlowView) OpenRunInputs(s FlowService, name, definition string, inputs map[string]string, w, h int) tea.Cmd {
	v.OpenDraft(s, name, definition, definition, w, h)
	v.inputs = maps.Clone(inputs)
	return v.openInputs()
}

func (v *FlowView) openInputs() tea.Cmd {
	v.page, v.keys, v.field = flowPageInputs, v.plan.InputNames(), 0
	for v.field < len(v.keys) && strings.TrimSpace(v.inputs[v.keys[v.field]]) != "" {
		v.field++
	}
	return v.showInput()
}

func (v *FlowView) showInput() tea.Cmd {
	v.editor.Blur()
	v.resizeInputs()
	if v.field == len(v.keys) {
		v.body.GotoTop()
		return nil
	}
	v.editor.Placeholder = "Enter a value…"
	v.editor.SetValue(v.inputs[v.keys[v.field]])
	return v.editor.Focus()
}

func (v *FlowView) resizeInputs() {
	v.editor.SetWidth(max(1, min(72, v.width-8)))
	v.editor.SetHeight(max(1, min(5, v.height-13)))
	v.body.SetHeight(max(1, v.height-8))
	if v.field == len(v.keys) {
		v.body.SetContent(v.renderInputSummary())
	}
}

func (v *FlowView) keepInput() {
	if v.field < len(v.keys) {
		v.inputs[v.keys[v.field]] = v.editor.Value()
	}
}

func (v *FlowView) submitInput() tea.Cmd {
	current, err := v.service.Definition(v.name)
	if err != nil || current != v.source {
		v.saved = false
		v.showReview()
		v.message = "Definition changed since review; reopen it before running"
		v.setError(err)
		return nil
	}
	_, err = v.service.Launch(v.name, v.inputs, v.width)
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
		v.page, v.cursor = flowPageExecutions, 0
		if len(choices) == 1 {
			return v.handleExecutions("enter")
		}
	case "s":
		if v.record != nil && v.record.Status == flow.RunRunning {
			v.confirm = flowConfirmStop
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
		v.confirm = flowConfirmResume
		v.body.SetContent(v.resumePreview())
		v.body.GotoTop()
	}
	return nil
}

func (v *FlowView) handleConfirm(key string) tea.Cmd {
	if key == "enter" && v.confirm == flowConfirmSave {
		key = "y"
	}
	var started tea.Cmd
	switch key {
	case "y":
		switch v.confirm {
		case flowConfirmSave:
			if v.validate() {
				err := v.service.SaveDefinition(v.name, v.source, v.baseline)
				v.setError(err)
				if err == nil {
					v.baseline, v.saved, v.page = v.source, true, flowPageReview
				}
			}
		case flowConfirmStop:
			v.setError(v.service.StopRun(v.record.ID))
			v.refreshRun()
		case flowConfirmResume:
			_, err := v.service.ResumeRun(v.record.ID, v.resumeFrom, v.width)
			v.setError(err)
			if err == nil {
				started = func() tea.Msg { return FlowStartedMsg{} }
			}
			v.refreshRun()
		case flowConfirmDiscard:
			v.close()
		}
		v.confirm = ""
	case "n", "esc":
		v.confirm = ""
	default:
		v.scroll(key)
	}
	if v.confirm == "" && v.page == flowPageReview {
		v.showReview()
	}
	return tea.Batch(v.tick(), started)
}

func (v *FlowView) back() {
	switch v.page {
	case flowPageEdit:
		v.source = v.editor.Value()
		v.saved = v.baseline != "" && v.source == v.baseline
		v.validate()
		v.showReview()
	case flowPageReview:
		if !v.saved {
			v.confirm = flowConfirmDiscard
		} else {
			v.close()
		}
	case flowPageExecutions:
		v.page = flowPageRun
	case flowPageInputs:
		v.keepInput()
		v.editor.Blur()
		v.showReview()
	case flowPageDetails:
		v.showReview()
	default:
		v.close()
	}
}

func (v *FlowView) close() {
	v.active = false
	v.draftAt = time.Time{}
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
	record, err := v.service.GetRun(v.record.ID)
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
