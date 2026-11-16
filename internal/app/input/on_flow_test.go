package input

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/flow"
)

const flowUISource = "---\nname: sample\n---\n\n```mermaid\nflowchart TD\n a --> b\n a --> c\n b --> d\n c --> d\n```\n\n## a\nUse {{input.topic}}\n\n## b\nUse {{a}}\n\n## c\nUse {{a}}\n\n## d\nUse {{b}} and {{c}}\n"

type flowUIService struct {
	r                                      flow.RunRecord
	saves, launches, stops, resumes, polls int
	err                                    error
	from                                   string
	source                                 string
	values                                 map[string]string
}

func (s *flowUIService) Launch(_ string, in map[string]string, _ int) (string, string, error) {
	s.launches++
	s.values = in
	return "bounds", "task", s.err
}
func (s *flowUIService) Runs() ([]flow.RunRecord, error) {
	return []flow.RunRecord{s.r}, nil
}
func (s *flowUIService) InspectRun(string) (*flow.RunRecord, error) {
	s.polls++
	r := s.r
	return &r, nil
}
func (s *flowUIService) ResumeRun(_ string, from string) (string, string, error) {
	s.from = from
	s.resumes++
	s.r.Status = flow.RunRunning
	return "", "task", s.err
}
func (s *flowUIService) StopRun(string) error { s.stops++; return s.err }
func (s *flowUIService) SavedDefinitions() []flow.Saved {
	return []flow.Saved{{Name: "sample", Path: "/global/sample.md"}}
}
func (s *flowUIService) Definition(string) (string, error) {
	if s.source != "" {
		return s.source, nil
	}
	return flowUISource, nil
}
func (s *flowUIService) SaveDefinition(string, string, string) error { s.saves++; return s.err }
func flowUIFixture() *flowUIService {
	return &flowUIService{r: flow.RunRecord{ID: "run", Name: "sample", TaskID: "task", Definition: flowUISource, Status: flow.RunRunning, StartedAt: time.Now().Add(-time.Minute), Nodes: map[string]flow.Checkpoint{"a": {Status: flow.StatusSucceeded, Duration: 7 * time.Second}, "b": {Status: flow.StatusRunning, StartedAt: time.Now().Add(-time.Second)}, "c": {Status: flow.StatusPending}, "d": {Status: flow.StatusPending}}, Activity: map[string][]string{"b": {"old", "one", "two", "latest"}}}}
}
func flowKey(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: rune(k[0]), Text: k}
}
func TestFlowGraphMarkersAndRealLiveness(t *testing.T) {
	s := flowUIFixture()
	var v FlowView
	v.OpenRun(s, "run", 100, 45)
	graph := ansi.Strip(v.renderGraph())
	for _, want := range []string{"● a", "● b", "○ c", "○ d", "▼", "─"} {
		if !strings.Contains(graph, want) {
			t.Fatalf("missing %q in graph %s", want, graph)
		}
	}
	for _, bad := range []string{"running", "succeeded", "pending", "7s"} {
		if strings.Contains(graph, bad) {
			t.Fatalf("normal graph leaks %q", bad)
		}
	}
	first := v.marker("b")
	v.phase++
	if first == v.marker("b") {
		t.Fatal("live running circle brightness must pulse")
	}
	if !strings.Contains(ansi.Strip(first), "●") {
		t.Fatal("running circle must stay solid")
	}
	v.record.Status = flow.RunInterrupted
	first = v.marker("b")
	v.phase++
	if first != v.marker("b") {
		t.Fatal("interrupted node must not animate")
	}
	if !strings.Contains(v.Render(), "INTERRUPTED") {
		t.Fatal("exception meaning missing")
	}
}
func TestFlowTailFocusAndPoll(t *testing.T) {
	s := flowUIFixture()
	var v FlowView
	v.OpenRun(s, "run", 100, 45)
	detail := ansi.Strip(v.renderDetail())
	if strings.Contains(detail, "old") || strings.Contains(detail, "latest") || !strings.Contains(detail, "native executions") {
		t.Fatal(detail)
	}
	v.HandleKeypress(flowKey("down"))
	pinned := v.selected
	v.Update(flowTick{v.generation})
	if v.selected != pinned || v.follow {
		t.Fatal("manual selection did not pin")
	}
	v.HandleKeypress(flowKey("f"))
	if v.nodeID() != "b" || !v.follow {
		t.Fatal("follow did not choose active node")
	}
	polls := s.polls
	v.Update(flowTick{v.generation - 1})
	if polls != s.polls {
		t.Fatal("stale generation polled")
	}
	v.HandleKeypress(flowKey("esc"))
	v.Update(flowTick{v.generation})
	if polls != s.polls {
		t.Fatal("closed view polled")
	}
}
func TestFlowDraftValidationSaveAndInputs(t *testing.T) {
	s := flowUIFixture()
	var v FlowView
	v.OpenDraft(s, "sample", flowUISource, "", 90, 40)
	v.HandleKeypress(flowKey("r"))
	if v.page != "review" {
		t.Fatal("unsaved draft runnable")
	}
	v.HandleKeypress(flowKey("s"))
	if v.confirm != "save" || !strings.Contains(ansi.Strip(v.body.View()), "name: sample") {
		t.Fatal("save diff missing")
	}
	v.HandleKeypress(flowKey("y"))
	if !v.saved || s.saves != 1 {
		t.Fatal("save failed")
	}
	v.HandleKeypress(flowKey("r"))
	if v.page != "inputs" || len(v.keys) != 1 || v.keys[0] != "topic" {
		t.Fatal("missing inferred input")
	}
	if help := ansi.Strip(v.Render()); !strings.Contains(help, "{{input.topic}} takes the value below") || !strings.Contains(help, "used by a") {
		t.Fatal("run input does not explain how its value is used")
	}
	v.HandleKeypress(flowKey("enter"))
	if s.launches != 0 || v.message == "" {
		t.Fatal("blank input launched")
	}
	v.HandlePaste("my topic")
	v.HandleKeypress(flowKey("enter"))
	if s.launches != 1 || s.values["topic"] != "my topic" || v.IsActive() {
		t.Fatal("input launch did not enter run")
	}
	v.OpenDraft(s, "broken", "not a flow", "", 90, 40)
	v.HandleKeypress(flowKey("s"))
	if v.confirm != "" || s.saves != 1 {
		t.Fatal("invalid definition saved")
	}
	v.OpenDraft(s, "sample", flowUISource, "", 90, 40)
	s.err = errors.New("baseline conflict")
	v.HandleKeypress(flowKey("s"))
	v.HandleKeypress(flowKey("y"))
	if v.saved || !strings.Contains(v.Render(), "baseline conflict") {
		t.Fatal("service save conflict hidden")
	}
}
func TestFlowEditorsKeepActionKeysAsText(t *testing.T) {
	for _, page := range []string{"edit", "inputs"} {
		t.Run(page, func(t *testing.T) {
			s := flowUIFixture()
			var v FlowView
			v.OpenDraft(s, "sample", flowUISource, flowUISource, 90, 40)
			key := "e"
			if page == "inputs" {
				key = "r"
			}
			v.HandleKeypress(flowKey(key))
			before := v.editor.Value()
			v.HandleKeypress(flowKey("s"))
			if v.page != page || v.editor.Value() == before || v.confirm != "" || s.saves != 0 || s.stops != 0 {
				t.Fatal("typing in an editor triggered a flow action")
			}
		})
	}
}

func TestFlowResponsiveInspectAndConfirmations(t *testing.T) {
	s := flowUIFixture()
	c := s.r.Nodes["b"]
	c.Output = strings.Repeat("output\n", 50)
	c.Error = "pinned failure"
	c.History = []flow.Attempt{{Number: 1, Output: "prior output"}}
	c.Workers = []flow.WorkerCheckpoint{{Index: 0, Label: "worker", State: flow.Checkpoint{Output: "worker result", Execution: flow.Execution{TaskID: "worker-task", SessionID: "worker-session"}}}}
	s.r.Nodes["b"] = c
	var v FlowView
	v.OpenRun(s, "run", 100, 40)
	v.Resize(38, 24)
	out := v.Render()
	for _, line := range strings.Split(out, "\n") {
		if ansi.StringWidth(line) > 38 {
			t.Fatalf("line exceeds dimensions: %q", line)
		}
	}
	if strings.Contains(v.renderGraph(), "▼") {
		t.Fatal("narrow graph should use layered list")
	}
	v.HandleKeypress(flowKey("enter"))
	if !strings.Contains(v.renderExecutions(), "Worker 1") || strings.Contains(v.Render(), "worker result") {
		t.Fatal("inspection must list execution identities without copying output")
	}
	v.body.ScrollDown(8)
	offset := v.body.YOffset()
	v.Update(flowTick{v.generation})
	if v.body.YOffset() != offset {
		t.Fatal("poll moved inspection scroll")
	}
	if strings.Contains(v.Render(), "pinned failure") {
		t.Fatal("graph inspection copied native task error rendering")
	}
	v.HandleKeypress(flowKey("esc"))
	v.HandleKeypress(flowKey("s"))
	if s.stops != 0 {
		t.Fatal("stop was not confirmed")
	}
	v.HandleKeypress(flowKey("n"))
	if s.stops != 0 {
		t.Fatal("stop cancel failed")
	}
	v.record.Status = flow.RunFailed
	v.HandleKeypress(flowKey("r"))
	preview := v.resumePreview()
	if !strings.Contains(preview, "a: reuse") || !strings.Contains(preview, "b: rerun") || !strings.Contains(preview, "d: rerun") || !strings.Contains(preview, "side effects") {
		t.Fatal(preview)
	}
	if s.resumes != 0 {
		t.Fatal("automatic retry")
	}
	v.HandleKeypress(flowKey("y"))
	if s.resumes != 1 || s.from != "" || v.record.Status != flow.RunRunning {
		t.Fatal("confirmed resume not refreshed")
	}
}
func TestFlowRunRefusesSourceChangedAfterReview(t *testing.T) {
	s := flowUIFixture()
	var v FlowView
	v.OpenDraft(s, "sample", flowUISource, flowUISource, 90, 40)
	review := ansi.Strip(v.body.View())
	if !strings.Contains(review, "Step 1/4 · a") || !strings.Contains(review, "{{input.topic}}") {
		t.Fatal("review is missing the selected step and its input binding")
	}
	v.HandleKeypress(flowKey("r"))
	v.HandlePaste("topic")
	s.source = flowUISource + "\nExternal edit\n"
	v.HandleKeypress(flowKey("enter"))
	if s.launches != 0 || v.saved || v.page != "review" || !strings.Contains(v.message, "changed since review") {
		t.Fatal("changed source was launched without another review")
	}
}

func TestFlowPollStopsWhenSettledAndRestartsOnRerun(t *testing.T) {
	s := flowUIFixture()
	var v FlowView
	if v.OpenRun(s, "run", 90, 40) == nil {
		t.Fatal("live run did not start polling")
	}
	if v.tick() != nil {
		t.Fatal("duplicate poll loop scheduled")
	}
	s.r.Status = flow.RunSucceeded
	s.r.Nodes["d"] = flow.Checkpoint{Status: flow.StatusSucceeded, Output: "done"}
	if cmd, _ := v.Update(flowTick{v.generation}); cmd != nil {
		t.Fatal("settled view kept polling and animating")
	}
	v.HandleKeypress(flowKey("R"))
	if v.confirm != "resume" || v.resumeFrom != "d" {
		t.Fatal("explicit rerun lost its selected origin")
	}
	if cmd := v.HandleKeypress(flowKey("y")); cmd == nil || s.from != "d" {
		t.Fatal("rerun did not restart polling from the selected node")
	}
}

func TestFlowCompletedShowsResultNotActivity(t *testing.T) {
	s := flowUIFixture()
	s.r.Status = flow.RunSucceeded
	s.r.Output = "all graph statuses summary"
	s.r.Nodes["d"] = flow.Checkpoint{Status: flow.StatusSucceeded, Output: "final result"}
	var v FlowView
	v.OpenRun(s, "run", 90, 35)
	detail := v.renderDetail()
	if strings.Contains(detail, "final result") || strings.Contains(detail, "latest") {
		t.Fatal(detail)
	}
}
