package input

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/flow"
)

func flowEntryPrimary(t *testing.T, v *FlowView, key, label string) {
	t.Helper()
	lines := strings.Split(ansi.Strip(v.Render()), "\n")
	action := ansi.Strip(v.renderPrimaryAction())
	if len(lines) < 3 || lines[2] != action || !strings.Contains(action, key) || !strings.Contains(action, label) {
		t.Fatalf("primary action %q %q must be directly below title:\n%s", key, label, v.Render())
	}
}

func TestFlowHubIdentifiesSavedBlueprints(t *testing.T) {
	s := flow.NewScheduler(nil)
	s.SetSearchPaths([]string{filepath.Join(t.TempDir(), "workflows")})
	names := []string{"release-notes", "release_notes"}
	for _, name := range names {
		if err := s.SaveDefinition(name, flowDraft(name, "write", "## write\nDraft "+name+"\n"), ""); err != nil {
			t.Fatal(err)
		}
	}
	for i, name := range names {
		var v FlowView
		v.Open(s, 60, 20)
		for range i + 1 {
			v.HandleKeypress(flowKey("down"))
		}
		v.Update(flowTick{v.generation})
		if screen := ansi.Strip(v.Render()); !strings.Contains(screen, name) {
			t.Errorf("cannot identify %s in hub:\n%s", name, screen)
		}
		v.HandleKeypress(flowKey("enter"))
		if v.page != flowPageReview || v.name != name || !strings.Contains(v.source, "Draft "+name) {
			t.Fatalf("wrong selection for %s: %s %s", name, v.page, v.name)
		}
	}
}

func TestFlowEntryHubSelection(t *testing.T) {
	for _, key := range []string{"enter", "n"} {
		t.Run(key, func(t *testing.T) {
			s := flowUIFixture()
			var v FlowView
			v.Open(s, 80, 24)
			flowEntryPrimary(t, &v, "Enter", "Create a flow")
			if v.cursor != 0 {
				t.Fatal("hub must select creation first")
			}
			v.HandleKeypress(flowKey(key))
			if v.page != "goal" || !v.editor.Focused() || s.saves != 0 || s.launches != 0 {
				t.Fatal("creation must open only the goal editor")
			}
			v.HandleKeypress(flowKey("esc"))
			if v.IsActive() {
				t.Fatal("Escape must close goal entry")
			}
		})
	}
	var v FlowView
	s := flowUIFixture()
	v.Open(s, 80, 24)
	v.HandleKeypress(flowKey("down"))
	v.HandleKeypress(flowKey("enter"))
	if v.page != "review" || !v.saved || v.name != "sample" {
		t.Fatal("first saved row must follow creation")
	}
	v.Open(s, 80, 24)
	for range 20 {
		v.HandleKeypress(flowKey("down"))
	}
	if v.cursor != 2 {
		t.Fatalf("last hub index = %d, want 2", v.cursor)
	}
	v.HandleKeypress(flowKey("enter"))
	if v.page != "run" || v.record == nil || v.record.ID != "run" {
		t.Fatal("recent run must follow saved definitions")
	}
}

func TestFlowEntryGoalRequestAndRepair(t *testing.T) {
	s := flowUIFixture()
	var v FlowView
	v.Open(s, 80, 24)
	v.HandleKeypress(flowKey("enter"))
	flowEntryPrimary(t, &v, "Enter", "Generate blueprint")
	if cmd := v.HandleKeypress(flowKey("enter")); cmd != nil || v.page != "goal" || v.message == "" {
		t.Fatal("empty goal must validate locally")
	}
	for _, k := range []string{"e", "r", "s"} {
		v.HandleKeypress(flowKey(k))
	}
	v.HandlePaste(" pasted")
	for _, key := range []tea.KeyMsg{
		flowKey("shift+enter"),
		tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt},
		tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl},
	} {
		v.HandleKeypress(key)
		v.HandlePaste("line")
	}
	want := "ers pasted\nline\nline\nline"
	if v.editor.Value() != want {
		t.Fatalf("goal text = %q, want %q", v.editor.Value(), want)
	}
	v.editor.SetValue("  " + want + " \n")
	cmd := v.HandleKeypress(flowKey("enter"))
	if cmd == nil {
		t.Fatal("goal did not request generation")
	}
	request, ok := cmd().(FlowDraftRequestMsg)
	if !ok || request.Goal != want || v.page != "generate" || s.saves != 0 || s.launches != 0 {
		t.Fatalf("unexpected authoring request: %#v", request)
	}
	v.DraftReady(request.Generation, "", errors.New("generation failed"))
	v.HandleKeypress(flowKey("e"))
	if v.page != "goal" || v.editor.Value() != want {
		t.Fatal("repair must retain the failed goal")
	}
}

func TestFlowEntryReviewEnterProgression(t *testing.T) {
	for _, confirm := range []string{"enter", "y"} {
		t.Run(string(confirm), func(t *testing.T) {
			s := flowUIFixture()
			var v FlowView
			v.OpenDraft(s, "sample", flowUISource, "", 80, 32)
			flowEntryPrimary(t, &v, "Enter", "Run flow…")
			v.HandleKeypress(flowKey("s"))
			flowEntryPrimary(t, &v, "Enter", "Confirm save")
			if v.confirm != "save" || s.saves != 0 {
				t.Fatal("Enter must preview before saving")
			}
			v.HandleKeypress(flowKey(confirm))
			if s.saves != 1 || s.launches != 0 || v.page != "review" || !v.saved {
				t.Fatal("saving should return to review without executing")
			}
			v.HandleKeypress(flowKey("enter"))
			flowEntryPrimary(t, &v, "Enter", "Continue")
			v.HandlePaste("topic")
			v.HandleKeypress(flowKey("enter"))
			flowEntryPrimary(t, &v, "Enter", "Run flow")
			if s.launches != 0 {
				t.Fatal("input completion must not launch")
			}
			cmd := v.HandleKeypress(flowKey("enter"))
			if cmd == nil {
				t.Fatal("summary Enter must launch")
			}
			if _, ok := cmd().(FlowStartedMsg); !ok || s.launches != 1 || v.IsActive() {
				t.Fatal("launch must emit started and close")
			}
		})
	}
	var v FlowView
	v.OpenDraft(flowUIFixture(), "broken", "invalid", "", 80, 24)
	flowEntryPrimary(t, &v, "Enter", "Edit blueprint")
	v.HandleKeypress(flowKey("enter"))
	if v.page != "edit" || v.editor.Value() != "invalid" {
		t.Fatal("invalid review must open source repair")
	}
}

func TestFlowEntrySummaryPreservesDriftGuard(t *testing.T) {
	s := flowUIFixture()
	var v FlowView
	v.OpenDraft(s, "sample", flowUISource, flowUISource, 80, 24)
	v.HandleKeypress(flowKey("enter"))
	v.HandlePaste("topic")
	v.HandleKeypress(flowKey("enter"))
	s.source = strings.Replace(flowUISource, "Use {{input.topic}}", "Changed", 1)
	if cmd := v.HandleKeypress(flowKey("enter")); cmd != nil || s.launches != 0 || v.page != "review" || v.saved || !strings.Contains(v.message, "changed") {
		t.Fatal("summary Enter bypassed source drift guard")
	}
}

func TestFlowEntryDestructiveConfirmationsRequireY(t *testing.T) {
	for _, confirm := range []flowConfirm{flowConfirmDiscard, flowConfirmStop, flowConfirmResume} {
		t.Run(string(confirm), func(t *testing.T) {
			s := flowUIFixture()
			var v FlowView
			v.OpenRun(s, "run", 80, 24)
			v.confirm = confirm
			lines := strings.Split(ansi.Strip(v.Render()), "\n")
			if footer := lines[len(lines)-1]; !strings.Contains(footer, "Esc cancel") || strings.Contains(footer, "Esc close") {
				t.Errorf("modal footer: %s", footer)
			}
			v.HandleKeypress(flowKey("esc"))
			if v.confirm != "" || !v.IsActive() || s.stops != 0 || s.resumes != 0 {
				t.Fatal("Esc did not safely cancel")
			}
			v.confirm = confirm
			action := ansi.Strip(v.renderPrimaryAction())
			if !strings.Contains(action, "y") || strings.Contains(action, "Enter") {
				t.Fatalf("destructive action must advertise y: %s", action)
			}
			v.HandleKeypress(flowKey("enter"))
			if v.confirm != confirm || !v.IsActive() || s.stops != 0 || s.resumes != 0 {
				t.Fatal("Enter confirmed a destructive action")
			}
			v.HandleKeypress(flowKey("y"))
			if v.confirm != "" || (confirm == "discard" && v.IsActive()) || (confirm == "stop" && s.stops != 1) || (confirm == "resume" && s.resumes != 1) {
				t.Fatal("y no longer confirms destructive action")
			}
		})
	}
}

func TestFlowRunFocusControls(t *testing.T) {
	for _, width := range []int{40, 80} {
		var v FlowView
		v.OpenRun(flowUIFixture(), "run", width, 24)
		check := func(want []string, absent string) {
			t.Helper()
			out := ansi.Strip(v.Render())
			for _, text := range want {
				if !strings.Contains(out, text) {
					t.Errorf("width %d missing %q:\n%s", width, text, out)
				}
			}
			if strings.Contains(out, absent) {
				t.Errorf("unexpected %q:\n%s", absent, out)
			}
		}
		check([]string{"Following", "Esc close", "s stop", "↑↓ pin"}, "f follow")
		v.HandleKeypress(flowKey("j"))
		id := v.nodeID()
		v.Update(flowTick{v.generation})
		if v.nodeID() != id || v.follow {
			t.Fatal("poll moved pinned focus")
		}
		check([]string{"Pinned", "Esc close", "f follow", "No execution"}, "Enter inspect")
		v.HandleKeypress(flowKey("f"))
		if !v.follow || v.nodeID() != "b" {
			t.Fatal("did not follow running node")
		}
		v.record.Status = flow.RunFailed
		check([]string{"Esc close", "r resume", "R rerun"}, "f follow")
		v.HandleKeypress(flowKey("r"))
		if v.confirm != flowConfirmResume {
			t.Fatal("resume did not open confirmation")
		}
	}
}

func TestFlowEntryInspectionAvailability(t *testing.T) {
	var v FlowView
	v.OpenRun(flowUIFixture(), "run", 80, 24)
	for _, state := range []flow.Checkpoint{
		{Outcome: flow.Outcome{Status: flow.StatusPending}},
		{Outcome: flow.Outcome{Status: flow.StatusSkipped}},
		{Outcome: flow.Outcome{Status: flow.StatusRunning}},
		{Outcome: flow.Outcome{Status: flow.StatusSucceeded, Execution: flow.Execution{TaskID: "legacy"}}},
	} {
		v.record.Nodes[v.nodeID()] = state
		action := ansi.Strip(v.renderPrimaryAction())
		if strings.Contains(action, "Enter") || !strings.Contains(action, "No execution") || strings.Contains(ansi.Strip(v.Render()), "Enter inspect") {
			t.Fatalf("unavailable execution advertises inspection: %s", action)
		}
	}
	v.record.Nodes[v.nodeID()] = flow.Checkpoint{Outcome: flow.Outcome{Execution: flow.Execution{TaskID: "child", SessionID: "session"}}}
	flowEntryPrimary(t, &v, "Enter", "Inspect selected step")
	v.page = flowPageExecutions
	flowEntryPrimary(t, &v, "Enter", "Open native task")
	v.cursor = 1
	if strings.Contains(ansi.Strip(v.renderPrimaryAction()), "Enter") {
		t.Fatal("missing execution choice advertises an action")
	}
}

func TestFlowEntryGoalNarrowScreens(t *testing.T) {
	for _, height := range []int{8, 18, 32} {
		t.Run(fmt.Sprint(height), func(t *testing.T) {
			var v FlowView
			v.Open(flowUIFixture(), 40, height)
			v.HandleKeypress(flowKey("enter"))
			v.HandleKeypress(flowKey("enter"))
			v.HandlePaste("Visible goal")
			flowEntryPrimary(t, &v, "Enter", "Generate blueprint")
			rendered := v.Render()
			if !strings.Contains(ansi.Strip(rendered), "Visible goal") {
				t.Fatalf("goal editor hidden:\n%s", rendered)
			}
			lines := strings.Split(rendered, "\n")
			if len(lines) > height {
				t.Fatalf("render height %d exceeds %d", len(lines), height)
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > 40 {
					t.Fatalf("line exceeds width 40: %q", line)
				}
			}
		})
	}
}
