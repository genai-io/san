package input

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

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

// WorkflowView owns navigation and interaction; rendering lives in on_workflow_view.go.
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
	v.page = "review"
	v.saved = baseline != "" && baseline == definition
	v.validate()
	v.body.SetContent(v.renderReview())
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
		v.body.SetContent(v.renderReview())
	}
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
	case "output":
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
		v.saved, v.page = false, "review"
		v.body.SetContent(v.renderReview())
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
		v.body.SetContent(v.renderReview())
	}
	return v.tick()
}

func (v *WorkflowView) back() {
	switch v.page {
	case "edit":
		v.source = v.editor.Value()
		v.saved = v.baseline != "" && v.source == v.baseline
		v.page = "review"
		v.validate()
		v.body.SetContent(v.renderReview())
	case "review":
		if !v.saved {
			v.confirm = "discard"
		} else {
			v.close()
		}
	case "output":
		v.page = "run"
	case "inputs":
		v.page = "review"
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
