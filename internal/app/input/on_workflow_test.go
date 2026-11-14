package input

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/workflow"
)

const workflowUISource = "---\nname: sample\n---\n\n```mermaid\nflowchart TD\n a --> b\n a --> c\n b --> d\n c --> d\n```\n\n## a\nUse {{input.topic}}\n\n## b\nUse {{a}}\n\n## c\nUse {{a}}\n\n## d\nUse {{b}} and {{c}}\n"

type workflowUIService struct {
	r                                      workflow.RunRecord
	saves, launches, stops, resumes, polls int
	err                                    error
	from                                   string
	source                                 string
	values                                 map[string]string
}

func (s *workflowUIService) Launch(_ string, in map[string]string, _ int) (string, string, error) {
	s.launches++
	s.values = in
	return "bounds", "task", s.err
}
func (s *workflowUIService) Runs() ([]workflow.RunRecord, error) {
	return []workflow.RunRecord{s.r}, nil
}
func (s *workflowUIService) InspectRun(string) (*workflow.RunRecord, error) {
	s.polls++
	r := s.r
	return &r, nil
}
func (s *workflowUIService) ResumeRun(_ string, from string) (string, string, error) {
	s.from = from
	s.resumes++
	s.r.Status = workflow.RunRunning
	return "", "task", s.err
}
func (s *workflowUIService) StopRun(string) error { s.stops++; return s.err }
func (s *workflowUIService) SavedDefinitions() []workflow.Saved {
	return []workflow.Saved{{Name: "sample", Path: "/global/sample.md"}}
}
func (s *workflowUIService) Definition(string) (string, error) {
	if s.source != "" {
		return s.source, nil
	}
	return workflowUISource, nil
}
func (s *workflowUIService) SaveDefinition(string, string, string) error { s.saves++; return s.err }
func workflowUIFixture() *workflowUIService {
	return &workflowUIService{r: workflow.RunRecord{ID: "run", Name: "sample", TaskID: "task", Definition: workflowUISource, Status: workflow.RunRunning, StartedAt: time.Now().Add(-time.Minute), Nodes: map[string]workflow.Checkpoint{"a": {Status: workflow.StatusSucceeded, Duration: 7 * time.Second}, "b": {Status: workflow.StatusRunning, StartedAt: time.Now().Add(-time.Second)}, "c": {Status: workflow.StatusPending}, "d": {Status: workflow.StatusPending}}, Activity: map[string][]string{"b": {"old", "one", "two", "latest"}}}}
}
func workflowKey(k string) tea.KeyMsg {
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
func TestWorkflowGraphMarkersAndRealLiveness(t *testing.T) {
	s := workflowUIFixture()
	var v WorkflowView
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
	v.record.Status = workflow.RunInterrupted
	first = v.marker("b")
	v.phase++
	if first != v.marker("b") {
		t.Fatal("interrupted node must not animate")
	}
	if !strings.Contains(v.Render(), "INTERRUPTED") {
		t.Fatal("exception meaning missing")
	}
}
func TestWorkflowTailFocusAndPoll(t *testing.T) {
	s := workflowUIFixture()
	var v WorkflowView
	v.OpenRun(s, "run", 100, 45)
	detail := ansi.Strip(v.renderDetail())
	if strings.Contains(detail, "old") || !strings.Contains(detail, "one\ntwo\nlatest") {
		t.Fatal(detail)
	}
	v.HandleKeypress(workflowKey("down"))
	pinned := v.selected
	v.Update(workflowTick{v.generation})
	if v.selected != pinned || v.follow {
		t.Fatal("manual selection did not pin")
	}
	v.HandleKeypress(workflowKey("f"))
	if v.nodeID() != "b" || !v.follow {
		t.Fatal("follow did not choose active node")
	}
	polls := s.polls
	v.Update(workflowTick{v.generation - 1})
	if polls != s.polls {
		t.Fatal("stale generation polled")
	}
	v.HandleKeypress(workflowKey("esc"))
	v.Update(workflowTick{v.generation})
	if polls != s.polls {
		t.Fatal("closed view polled")
	}
}
func TestWorkflowDraftValidationSaveAndInputs(t *testing.T) {
	s := workflowUIFixture()
	var v WorkflowView
	v.OpenDraft(s, "sample", workflowUISource, "", 90, 40)
	v.HandleKeypress(workflowKey("r"))
	if v.page != "review" {
		t.Fatal("unsaved draft runnable")
	}
	v.HandleKeypress(workflowKey("s"))
	if v.confirm != "save" || !strings.Contains(ansi.Strip(v.body.View()), "name: sample") {
		t.Fatal("save diff missing")
	}
	v.HandleKeypress(workflowKey("y"))
	if !v.saved || s.saves != 1 {
		t.Fatal("save failed")
	}
	v.HandleKeypress(workflowKey("r"))
	if v.page != "inputs" || len(v.keys) != 1 || v.keys[0] != "topic" {
		t.Fatal("missing inferred input")
	}
	v.HandleKeypress(workflowKey("enter"))
	if s.launches != 0 || v.message == "" {
		t.Fatal("blank input launched")
	}
	v.HandlePaste("my topic")
	v.HandleKeypress(workflowKey("enter"))
	if s.launches != 1 || s.values["topic"] != "my topic" || v.page != "run" {
		t.Fatal("input launch did not enter run")
	}
	v.OpenDraft(s, "broken", "not a workflow", "", 90, 40)
	v.HandleKeypress(workflowKey("s"))
	if v.confirm != "" || s.saves != 1 {
		t.Fatal("invalid definition saved")
	}
	v.OpenDraft(s, "sample", workflowUISource, "", 90, 40)
	s.err = errors.New("baseline conflict")
	v.HandleKeypress(workflowKey("s"))
	v.HandleKeypress(workflowKey("y"))
	if v.saved || !strings.Contains(v.Render(), "baseline conflict") {
		t.Fatal("service save conflict hidden")
	}
}
func TestWorkflowEditorsKeepActionKeysAsText(t *testing.T) {
	for _, page := range []string{"edit", "inputs"} {
		t.Run(page, func(t *testing.T) {
			s := workflowUIFixture()
			var v WorkflowView
			v.OpenDraft(s, "sample", workflowUISource, workflowUISource, 90, 40)
			key := "e"
			if page == "inputs" {
				key = "r"
			}
			v.HandleKeypress(workflowKey(key))
			before := v.editor.Value()
			v.HandleKeypress(workflowKey("s"))
			if v.page != page || v.editor.Value() == before || v.confirm != "" || s.saves != 0 || s.stops != 0 {
				t.Fatal("typing in an editor triggered a workflow action")
			}
		})
	}
}

func TestWorkflowResponsiveInspectAndConfirmations(t *testing.T) {
	s := workflowUIFixture()
	c := s.r.Nodes["b"]
	c.Output = strings.Repeat("output\n", 50)
	c.Error = "pinned failure"
	c.History = []workflow.Attempt{{Number: 1, Output: "prior output"}}
	c.Workers = []workflow.WorkerCheckpoint{{Index: 0, Label: "worker", State: workflow.Checkpoint{Output: "worker result"}}}
	s.r.Nodes["b"] = c
	var v WorkflowView
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
	v.HandleKeypress(workflowKey("enter"))
	if !strings.Contains(v.inspectOutput(), "prior output") || !strings.Contains(v.inspectOutput(), "worker result") {
		t.Fatal("full history missing")
	}
	v.body.ScrollDown(8)
	offset := v.body.YOffset()
	v.Update(workflowTick{v.generation})
	if v.body.YOffset() != offset {
		t.Fatal("poll moved inspection scroll")
	}
	if !strings.Contains(v.Render(), "pinned failure") {
		t.Fatal("node error scrolled away")
	}
	v.HandleKeypress(workflowKey("esc"))
	v.HandleKeypress(workflowKey("s"))
	if s.stops != 0 {
		t.Fatal("stop was not confirmed")
	}
	v.HandleKeypress(workflowKey("n"))
	if s.stops != 0 {
		t.Fatal("stop cancel failed")
	}
	v.record.Status = workflow.RunFailed
	v.HandleKeypress(workflowKey("r"))
	preview := v.resumePreview()
	if !strings.Contains(preview, "a: reuse") || !strings.Contains(preview, "b: rerun") || !strings.Contains(preview, "d: rerun") || !strings.Contains(preview, "side effects") {
		t.Fatal(preview)
	}
	if s.resumes != 0 {
		t.Fatal("automatic retry")
	}
	v.HandleKeypress(workflowKey("y"))
	if s.resumes != 1 || s.from != "" || v.record.Status != workflow.RunRunning {
		t.Fatal("confirmed resume not refreshed")
	}
}
func TestWorkflowRunRefusesSourceChangedAfterReview(t *testing.T) {
	s := workflowUIFixture()
	var v WorkflowView
	v.OpenDraft(s, "sample", workflowUISource, workflowUISource, 90, 40)
	if !strings.Contains(ansi.Strip(v.body.View()), "up to 4 agent runs") {
		t.Fatal("review is missing the execution bound")
	}
	v.HandleKeypress(workflowKey("r"))
	v.HandlePaste("topic")
	s.source = workflowUISource + "\nExternal edit\n"
	v.HandleKeypress(workflowKey("enter"))
	if s.launches != 0 || v.saved || v.page != "review" || !strings.Contains(v.message, "changed since review") {
		t.Fatal("changed source was launched without another review")
	}
}

func TestWorkflowPollStopsWhenSettledAndRestartsOnRerun(t *testing.T) {
	s := workflowUIFixture()
	var v WorkflowView
	if v.OpenRun(s, "run", 90, 40) == nil {
		t.Fatal("live run did not start polling")
	}
	if v.tick() != nil {
		t.Fatal("duplicate poll loop scheduled")
	}
	s.r.Status = workflow.RunSucceeded
	s.r.Nodes["d"] = workflow.Checkpoint{Status: workflow.StatusSucceeded, Output: "done"}
	if cmd, _ := v.Update(workflowTick{v.generation}); cmd != nil {
		t.Fatal("settled view kept polling and animating")
	}
	v.HandleKeypress(workflowKey("R"))
	if v.confirm != "resume" || v.resumeFrom != "d" {
		t.Fatal("explicit rerun lost its selected origin")
	}
	if cmd := v.HandleKeypress(workflowKey("y")); cmd == nil || s.from != "d" {
		t.Fatal("rerun did not restart polling from the selected node")
	}
}

func TestWorkflowCompletedShowsResultNotActivity(t *testing.T) {
	s := workflowUIFixture()
	s.r.Status = workflow.RunSucceeded
	s.r.Output = "all graph statuses summary"
	s.r.Nodes["d"] = workflow.Checkpoint{Status: workflow.StatusSucceeded, Output: "final result"}
	var v WorkflowView
	v.OpenRun(s, "run", 90, 35)
	detail := v.renderDetail()
	if !strings.Contains(detail, "final result") || strings.Contains(detail, "latest") {
		t.Fatal(detail)
	}
}
