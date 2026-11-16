package input

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/internal/workflow"
)

// WorkflowService is the UI's project-scoped workflow boundary.
type WorkflowService interface {
	Saved() string
	Launch(name string, inputs map[string]string, width int) (bounds, taskID string, err error)
	Runs() ([]workflow.RunRecord, error)
	InspectRun(id string) (*workflow.RunRecord, error)
	ResumeRun(id, from string) (bounds, taskID string, err error)
	StopRun(id string) error
	Definitions() []workflow.Saved
	Definition(name string) (string, error)
	SaveDefinition(name, definition, baseline string) error
}

type workflowTick struct{ generation uint64 }
type WorkflowView struct {
	active                                         bool
	service                                        WorkflowService
	width, height                                  int
	generation                                     uint64
	page, name, baseline, source, message, confirm string
	resumeFrom                                     string
	saved                                          bool
	definitions                                    []workflow.Saved
	runs                                           []workflow.RunRecord
	cursor, selected, field                        int
	record                                         *workflow.RunRecord
	plan                                           *workflow.Workflow
	editor                                         textarea.Model
	body                                           viewport.Model
	keys                                           []string
	inputs                                         map[string]string
	follow                                         bool
	polling                                        bool
	phase                                          int
}

func (v *WorkflowView) IsActive() bool { return v.active }
func (v *WorkflowView) init(s WorkflowService, w, h int) {
	g := v.generation + 1
	*v = WorkflowView{active: true, service: s, generation: g, follow: true, inputs: map[string]string{}}
	v.editor = newChromelessTextarea()
	v.body = viewport.New()
	v.Resize(w, h)
}
func (v *WorkflowView) Open(s WorkflowService, w, h int) tea.Cmd {
	v.init(s, w, h)
	v.page = "hub"
	v.definitions = s.Definitions()
	var err error
	v.runs, err = s.Runs()
	v.setError(err)
	return nil
}
func (v *WorkflowView) OpenDraft(s WorkflowService, name, definition, baseline string, w, h int) tea.Cmd {
	v.init(s, w, h)
	v.name = name
	v.source = definition
	v.baseline = baseline
	v.page = "review"
	v.saved = baseline != "" && baseline == definition
	v.validate()
	v.reviewContent()
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
	v.width = max(12, w)
	v.height = max(8, h)
	v.editor.SetWidth(max(1, w-4))
	v.editor.SetHeight(max(1, h-9))
	v.body.SetWidth(max(1, w-4))
	v.body.SetHeight(max(1, h-10))
	if v.page == "review" && v.confirm == "" {
		v.reviewContent()
	}
}
func (v *WorkflowView) tick() tea.Cmd {
	if !v.active || v.record == nil || v.record.Status != workflow.RunRunning || v.polling {
		return nil
	}
	v.polling = true
	g := v.generation
	return tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return workflowTick{g} })
}
func (v *WorkflowView) setError(err error) {
	if err != nil {
		v.message = err.Error()
	}
}
func (v *WorkflowView) validate() bool {
	var err error
	v.plan, err = workflow.Parse(v.source)
	if err != nil {
		v.message = err.Error()
		return false
	}
	v.message = ""
	return true
}

func (v *WorkflowView) prepareInputs() {
	v.keys = v.plan.InputNames()
	v.field = 0
	v.page = "inputs"
	v.editor.SetValue("")
	v.editor.Focus()
}
func (v *WorkflowView) focusActive() {
	if !v.follow || v.plan == nil || v.record == nil {
		return
	}
	if v.record.Status != workflow.RunRunning {
		for i, n := range v.plan.Nodes {
			if c := v.record.Nodes[n.ID]; v.record.Status != workflow.RunSucceeded && (c.Status == workflow.StatusFailed || c.Status == workflow.StatusRunning) {
				v.selected = i
				return
			}
		}
		for i, n := range v.plan.Nodes {
			if len(n.Downstream()) == 0 && v.record.Nodes[n.ID].Status == workflow.StatusSucceeded {
				v.selected = i
			}
		}
		return
	}
	for i, n := range v.plan.Nodes {
		if v.record.Nodes[n.ID].Status == workflow.StatusRunning {
			v.selected = i
			return
		}
	}
}
func (v *WorkflowView) Update(msg tea.Msg) (tea.Cmd, bool) {
	if t, ok := msg.(workflowTick); ok {
		if !v.active || t.generation != v.generation {
			return nil, true
		}
		v.polling = false
		v.phase++
		if v.record != nil && v.record.Status == workflow.RunRunning {
			r, err := v.service.InspectRun(v.record.ID)
			v.setError(err)
			if r != nil {
				v.record = r
				v.focusActive()
				if v.page == "output" && v.confirm == "" {
					offset := v.body.YOffset()
					v.body.SetContent(v.inspectOutput())
					v.body.SetYOffset(offset)
				}
			}
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
	k := msg.String()
	v.message = ""
	if v.confirm != "" {
		if k == "y" {
			switch v.confirm {
			case "save":
				if v.validate() {
					err := v.service.SaveDefinition(v.name, v.source, v.baseline)
					v.setError(err)
					if err == nil {
						v.baseline = v.source
						v.saved = true
						v.page = "review"
					}
				}
			case "stop":
				v.setError(v.service.StopRun(v.record.ID))
				v.refreshRun()
			case "discard":
				v.active = false
				v.generation++
			case "resume":
				_, _, err := v.service.ResumeRun(v.record.ID, v.resumeFrom)
				v.setError(err)
				v.refreshRun()
			}
			v.confirm = ""
		} else if k == "n" || k == "esc" {
			v.confirm = ""
		} else {
			v.scroll(k)
		}
		if v.confirm == "" && v.page == "review" {
			v.reviewContent()
		}
		return v.tick()
	}
	if k == "esc" {
		switch v.page {
		case "edit":
			v.source = v.editor.Value()
			v.saved = v.baseline != "" && v.source == v.baseline
			v.page = "review"
			v.validate()
			v.reviewContent()
		case "review":
			if !v.saved {
				v.confirm = "discard"
			} else {
				v.active = false
				v.generation++
			}
		case "output":
			v.page = "run"
		case "inputs":
			v.page = "review"
		default:
			v.active = false
			v.generation++
		}
		return nil
	}
	switch v.page {
	case "hub":
		total := len(v.definitions) + len(v.runs)
		switch k {
		case "up", "k":
			v.cursor = max(0, v.cursor-1)
		case "down", "j":
			v.cursor = min(max(0, total-1), v.cursor+1)
		case "enter":
			if v.cursor < len(v.definitions) {
				d := v.definitions[v.cursor]
				src, err := v.service.Definition(d.Name)
				if err != nil {
					v.setError(err)
					break
				}
				return v.OpenDraft(v.service, d.Name, src, src, v.width, v.height)
			}
			i := v.cursor - len(v.definitions)
			if i >= 0 && i < len(v.runs) {
				return v.OpenRun(v.service, v.runs[i].ID, v.width, v.height)
			}
		}
	case "review":
		switch k {
		case "e":
			v.page = "edit"
			v.editor.SetValue(v.source)
			return v.editor.Focus()
		case "r":
			if v.saved && v.validate() {
				v.prepareInputs()
			}
		case "s":
			if v.validate() {
				v.confirm = "save"
				v.body.SetContent(v.diffPreview())
				v.body.GotoTop()
			}
		default:
			v.scroll(k)
		}
	case "edit":
		if k == "ctrl+s" {
			v.source = v.editor.Value()
			v.saved = false
			if v.validate() {
				v.confirm = "save"
				v.body.SetContent(v.diffPreview())
			}
			return nil
		}
		var cmd tea.Cmd
		v.editor, cmd = v.editor.Update(msg)
		return cmd
	case "inputs":
		if k == "enter" {
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
				v.page = "review"
				v.reviewContent()
				v.message = "Definition changed since review; reopen it before running"
				v.setError(err)
				return nil
			}
			_, id, err := v.service.Launch(v.name, v.inputs, v.width)
			v.setError(err)
			if err == nil {
				return v.OpenTask(v.service, id, v.width, v.height)
			}
			return nil
		}
		var cmd tea.Cmd
		v.editor, cmd = v.editor.Update(msg)
		return cmd
	case "run":
		switch k {
		case "up", "k":
			v.follow = false
			v.selected = max(0, v.selected-1)
		case "down", "j":
			v.follow = false
			if v.plan != nil {
				v.selected = min(len(v.plan.Nodes)-1, v.selected+1)
			}
		case "f":
			v.follow = true
			v.focusActive()
		case "enter":
			v.follow = false
			v.page = "output"
			v.body.SetContent(v.inspectOutput())
			v.body.GotoTop()
		case "s":
			if v.record != nil && v.record.Status == workflow.RunRunning {
				v.confirm = "stop"
			}
		case "r", "R":
			if k == "r" && v.record != nil && v.record.Status == workflow.RunSucceeded {
				v.message = "Select a node and use R to rerun it"
				return nil
			}
			if v.record != nil && v.record.Status != workflow.RunRunning {
				v.resumeFrom = ""
				if k == "R" {
					v.resumeFrom = v.nodeID()
				}
				v.confirm = "resume"
				v.body.SetContent(v.resumePreview())
				v.body.GotoTop()
			}
		}
	case "output":
		switch k {
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
	return nil
}
func (v *WorkflowView) refreshRun() {
	if v.record == nil {
		return
	}
	r, err := v.service.InspectRun(v.record.ID)
	v.setError(err)
	if r != nil {
		v.record = r
		v.focusActive()
	}
}
func (v *WorkflowView) scroll(k string) {
	switch k {
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
func (v *WorkflowView) nodeID() string {
	if v.plan == nil || v.selected < 0 || v.selected >= len(v.plan.Nodes) {
		return ""
	}
	return v.plan.Nodes[v.selected].ID
}
func (v *WorkflowView) reviewContent() {
	if v.plan == nil {
		v.body.SetContent(v.source)
		return
	}
	preview := *v
	preview.record = &workflow.RunRecord{Nodes: map[string]workflow.Checkpoint{}}
	turns, bounds := v.plan.Bounds()
	plan := fmt.Sprintf("%d nodes · up to %d agent runs · max %d parallel", len(v.plan.Nodes), turns, v.plan.MaxParallel)
	if len(bounds) > 0 {
		plan += "\n" + strings.Join(bounds, "\n")
	}
	v.body.SetContent(plan + "\n\n" + preview.renderGraph() + "\n\nSource\n" + v.source)
}

func (v *WorkflowView) diffPreview() string {
	if v.baseline == v.source {
		return "No source changes"
	}
	meta := perm.GenerateDiff(v.name+".md", v.baseline, v.source)
	if v.baseline == "" {
		meta = perm.GeneratePreview(v.name+".md", v.source, true)
	}
	out, _ := conv.RenderFileDiff(meta.Lines, max(1, v.width-4), 0)
	return out
}
func (v *WorkflowView) inspectOutput() string {
	if v.record == nil {
		return ""
	}
	c := v.record.Nodes[v.nodeID()]
	var b strings.Builder
	fmt.Fprintln(&b, c.Output)
	if tail := v.record.Activity[v.nodeID()]; len(tail) > 0 {
		fmt.Fprintln(&b, "\nActivity (retained live tail)")
		for _, line := range tail {
			fmt.Fprintln(&b, line)
		}
	}
	for _, a := range c.History {
		fmt.Fprintf(&b, "\nAttempt %d · %s\n%s\n%s\n", a.Number, a.Status, a.Output, a.Error)
	}
	for _, w := range c.Workers {
		fmt.Fprintf(&b, "\nWorker %d %s · %s\n%s\n%s\n", w.Index, w.Label, w.State.Status, w.State.Output, w.State.Error)
		for _, a := range w.State.History {
			fmt.Fprintf(&b, "Attempt %d · %s\n%s\n%s\n", a.Number, a.Status, a.Output, a.Error)
		}
	}
	return b.String()
}
