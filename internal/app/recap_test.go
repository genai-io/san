package app

import (
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
)

func newRecapModel(t *testing.T) *model {
	m := fixedComposerModel(80, 40)
	m.services.Agent, m.services.Task = &agent.Runtime{}, task.NewManager()
	m.services.MCP = mcp.NewManagerForTest(nil)
	m.env.LLMProvider = &autopilotStubProvider{}
	m.env.SessionPermissions = setting.NewSessionPermissions()
	m.env.ApplyModePermissions(t.TempDir())
	m.conv.Messages = []core.ChatMessage{
		{Role: core.ChatUser, Content: "Fix it."},
		{Role: core.ChatAssistant, Content: "Done."},
	}
	return m
}

// A short absence earns nothing; a long one shows the recap written ahead, as
// a notice, without asking the model again.
func TestFocusShowsRecapWrittenAhead(t *testing.T) {
	m := newRecapModel(t)

	m.recap.awaySince = time.Now().Add(-time.Minute)
	if cmd := m.onFocus(); cmd != nil || len(m.conv.Messages) != 2 {
		t.Fatal("a minute away must not recap")
	}

	m.recap.awaySince = time.Now().Add(-23 * time.Minute)
	m.recap.ready = recapReadyMsg{sessionID: m.sessionID(), chainLen: m.chainLen(), text: "Now: x\nNext: y"}
	m.onFocus()
	last := m.conv.Messages[len(m.conv.Messages)-1]
	if last.Role != core.ChatNotice || !strings.HasPrefix(last.Content, "/recap away 23m\n\nNow: x") {
		t.Fatalf("recap notice = %q", last.Content)
	}
	if m.recap.busy || m.recap.ready.text != "" {
		t.Fatal("the recap written ahead must be used, not rewritten")
	}
}

// A recap written for another session, or one that failed, never lands.
func TestRecapForAnotherSessionIsDropped(t *testing.T) {
	m := newRecapModel(t)
	m.recap.busy, m.recap.show = true, true
	m.onRecapReady(recapReadyMsg{sessionID: "elsewhere", text: "Now: x"})
	if m.recap.busy || len(m.conv.Messages) != 2 {
		t.Fatal("another session's recap landed here")
	}
}

// The facts are San's own, most urgent first; the mission is the goal only
// while AutoPilot drives it.
func TestRecapFacts(t *testing.T) {
	m := newRecapModel(t)
	a := m.services.Tracker.Create("one", "", "", nil)
	m.services.Tracker.Create("two", "", "", nil)
	if err := m.services.Tracker.Update(a.ID, todo.WithStatus(todo.StatusCompleted)); err != nil {
		t.Fatal(err)
	}
	if got := m.recapFacts(65 * time.Minute); got != "away 1h05m · plan 1/2" {
		t.Fatalf("facts = %q", got)
	}
	m.env.AutoPilot.Mission = "ship it\nwith notes"
	if m.recapGoal() != "" {
		t.Fatal("a mission AutoPilot does not drive is not the goal")
	}
}

func TestDrivingChanged(t *testing.T) {
	on := false
	before := setting.AutoPilotSettings{}
	if drivingChanged(before, setting.AutoPilotSettings{RecapAfterMinutes: 10, Steers: setting.SteerSettings{Suggest: &on}}) {
		t.Error("Suggest and Recap work without AutoPilot mode")
	}
	if !drivingChanged(before, setting.AutoPilotSettings{Steers: setting.SteerSettings{TurnEnd: true}}) {
		t.Error("Continue only works while driving")
	}
}

// Coming back to a session still at work earns a recap: the screen shows the
// current step, not what happened while away. Writing it ahead waits for the
// session to go quiet, since a session at work keeps its cache warm.
func TestRecapWhileTheSessionWorks(t *testing.T) {
	m := newRecapModel(t)
	m.conv.Stream.Active = true

	m.recap.awaySince = time.Now().Add(-5 * time.Minute)
	if cmd := m.prepareRecap(); cmd != nil || m.recap.busy {
		t.Fatal("a recap was written ahead while the session works")
	}
	if cmd := m.onFocus(); cmd == nil || !m.recap.busy || !m.recap.show {
		t.Fatal("coming back to a working session wrote no recap")
	}
	if live := m.recapLive(); !strings.Contains(live, "writing") {
		t.Fatalf("no live row while the recap is written: %q", live)
	}
}

// A draft keeps an unasked recap away, but not one asked for: /recap runs
// while its own text is still in the composer.
func TestRecapDraftGatesOnlyUnaskedRecaps(t *testing.T) {
	m := newRecapModel(t)
	m.userInput.Textarea.SetValue("/recap")

	m.recap.awaySince = time.Now().Add(-10 * time.Minute)
	if cmd := m.onFocus(); cmd != nil {
		t.Fatal("a draft did not keep the unasked recap away")
	}
	if cmd := m.recapNow(); cmd == nil || !m.recap.show {
		t.Fatal("/recap did nothing with its own text in the composer")
	}
}

// Nobody reads ghost text in an unfocused terminal: the hint is skipped while
// away and fetched once on return.
func TestSuggestWaitsForFocus(t *testing.T) {
	m := newRecapModel(t)
	m.conv.Messages = append(m.conv.Messages, core.ChatMessage{Role: core.ChatAssistant, Content: "Also done."})

	m.recap.awaySince = time.Now()
	if cmd := m.startPromptSuggestion(); cmd != nil || !m.suggestOwed {
		t.Fatal("a hint was fetched for an unfocused terminal")
	}
	if m.onFocus(); m.suggestOwed {
		t.Fatal("the hint skipped while away was not fetched on return")
	}
}
