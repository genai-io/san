package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/san/internal/transcript"
)

func newRecapModel(t *testing.T) *model {
	m := fixedComposerModel(80, 40)
	m.services.Agent, m.services.Task = &agent.Runtime{}, task.NewManager()
	m.services.MCP = mcp.NewManagerForTest(nil)
	m.env.LLMProvider = &autopilotStubProvider{}
	m.env.SessionPermissions = setting.NewSessionPermissions()
	m.env.ApplyModePermissions(t.TempDir())
	for range recapMinTurns {
		addTurn(m)
	}
	return m
}

func addTurn(m *model) {
	m.conv.Messages = append(m.conv.Messages,
		core.ChatMessage{Role: core.ChatUser, Content: "Fix it."},
		core.ChatMessage{Role: core.ChatAssistant, Content: "Done."})
}

// A short absence earns nothing; a long one shows the recap written ahead, as
// a notice, without asking the model again.
func TestFocusShowsRecapWrittenAhead(t *testing.T) {
	m := newRecapModel(t)

	n := len(m.conv.Messages)
	m.unfocusedSince = time.Now().Add(-time.Minute)
	if cmd := m.onFocus(); cmd != nil || len(m.conv.Messages) != n {
		t.Fatal("a minute away must not recap")
	}

	m.unfocusedSince = time.Now().Add(-23 * time.Minute)
	m.recap.ready = recapReadyMsg{sessionID: m.sessionID(), chainLen: m.chainLen(), text: "Now: x\nNext: y"}
	m.onFocus()
	last := m.conv.Messages[len(m.conv.Messages)-1]
	if last.Role != core.ChatNotice || last.Content != "/recap \n\nNow: x\nNext: y" {
		t.Fatalf("recap notice = %q", last.Content)
	}
	if m.recap.busy || m.recap.ready.text != "" {
		t.Fatal("the recap written ahead must be used, not rewritten")
	}

	// The same terminal loses focus again after the work is already done.
	// Neither the warm-cache preparation nor returning eight minutes later
	// should generate a second recap over the same conversation.
	m.onBlur()
	m.unfocusedSince = time.Now().Add(-8 * time.Minute)
	if cmd := m.prepareRecap(); cmd != nil || m.recap.busy {
		t.Fatal("prepared a duplicate recap on the second absence")
	}
	if cmd := m.onFocus(); cmd != nil || len(m.conv.Messages) != n+1 {
		t.Fatal("returning again recapped the same finished work")
	}
}

// The recap is a persisted notice, not an agent message. After a real
// transcript round trip it still gates the next automatic recap.
func TestPersistedRecapDoesNotRepeatOnReturn(t *testing.T) {
	store, err := session.NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opened, err := store.Create(session.CreateOptions{ID: "recap-test"})
	if err != nil {
		t.Fatal(err)
	}
	var messages []core.Message
	for range recapMinTurns {
		messages = append(messages, core.UserMessage("Fix it.", nil), core.AssistantMessage("Done.", "", nil))
	}
	for i := range messages {
		messages[i].ID = core.NewMessageID()
	}
	if err := opened.Append(context.Background(), transcript.Record{Type: transcript.ConversationReplaced, Messages: messages}); err != nil {
		t.Fatal(err)
	}
	if err := opened.Append(context.Background(), transcript.Record{Type: transcript.NoticeAdded,
		Notice: &transcript.NoticeRecord{Text: conv.RecapNotice("away 1h04m", "Done; waiting for the user.")}}); err != nil {
		t.Fatal(err)
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
	history, err := store.Read("recap-test")
	if err != nil {
		t.Fatal(err)
	}
	m := newRecapModel(t)
	m.conv.Messages = store.ChatRows(history)
	if last := m.conv.Messages[len(m.conv.Messages)-1]; last.Role != core.ChatNotice || !strings.HasPrefix(last.Content, "/recap ") {
		t.Fatalf("restored recap notice = %+v", last)
	}
	m.onBlur()
	m.unfocusedSince = time.Now().Add(-8 * time.Minute)
	if cmd := m.prepareRecap(); cmd != nil || m.recap.busy {
		t.Fatal("prepared a recap over restored finished work")
	}
	if cmd := m.onFocus(); cmd != nil || m.recap.busy {
		t.Fatal("recapped again after resuming the session")
	}
}

// A recap written for another session, or one that failed, never lands.
func TestRecapForAnotherSessionIsDropped(t *testing.T) {
	m := newRecapModel(t)
	n := len(m.conv.Messages)
	m.recap.busy, m.recap.show = true, true
	m.onRecapReady(recapReadyMsg{sessionID: "elsewhere", text: "Now: x"})
	if m.recap.busy || len(m.conv.Messages) != n {
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
	if got := m.recapFacts(); got != "plan 1/2" {
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

// Returning while a turn is still using tools skips the automatic recap,
// even if one was written ahead. Finishing the turn does not resurrect it;
// a later absence while idle can earn a new one.
func TestRecapSkipsRunningTurn(t *testing.T) {
	m := newRecapModel(t)
	m.env.CWD = t.TempDir()
	m.services.sessionID = "test-recap"
	m.conv.Stream.Active = true

	m.unfocusedSince = time.Now().Add(-5 * time.Minute)
	if cmd := m.prepareRecap(); cmd != nil || m.recap.busy {
		t.Fatal("a recap was written ahead while the session works")
	}
	m.recap.ready = recapReadyMsg{sessionID: m.sessionID(), chainLen: m.chainLen(), text: "Next step"}
	n := len(m.conv.Messages)
	if cmd := m.onFocus(); cmd != nil || m.recap.busy || m.recap.show || len(m.conv.Messages) != n {
		t.Fatal("coming back during a running turn must skip the recap")
	}
	m.conv.Stream.Active = false
	m.handleStopHookResult(stopHookResultMsg{Result: core.Result{StopReason: core.StopEndTurn}})
	if len(m.conv.Messages) != n {
		t.Fatal("finishing the turn must not show the skipped recap")
	}
	m.unfocusedSince = time.Now().Add(-5 * time.Minute)
	m.onFocus()
	if got := m.conv.Messages[len(m.conv.Messages)-1]; got.Role != core.ChatNotice || got.Content != "/recap \n\nNext step" {
		t.Fatalf("a later idle return should still show a recap: %q", got.Content)
	}
}

// An unasked recap needs new ground: a few turns since the last one. /recap
// is always answered. The recap notice, not transient state, keeps this true
// after restoring a session too.
func TestRecapWaitsForNewTurns(t *testing.T) {
	m := newRecapModel(t)
	m.showRecap(recapReadyMsg{text: "Done; waiting for the user."})

	// A fresh model with the same restored conversation has no in-memory recap
	// state. The persisted notice alone must prevent another automatic recap.
	resumed := newRecapModel(t)
	resumed.conv.Messages = append([]core.ChatMessage(nil), m.conv.Messages...)
	for _, current := range []*model{m, resumed} {
		current.unfocusedSince = time.Now().Add(-10 * time.Minute)
		if cmd := current.prepareRecap(); cmd != nil || current.recap.busy {
			t.Fatal("prepared another recap over the same finished work")
		}
		if cmd := current.onFocus(); cmd != nil || current.recap.busy {
			t.Fatal("recapped again without new turns")
		}
	}
	addTurn(m)
	m.unfocusedSince = time.Now().Add(-10 * time.Minute)
	if cmd := m.onFocus(); cmd != nil {
		t.Fatal("recapped again after a single turn")
	}
	if cmd := m.recapNow(); cmd == nil {
		t.Fatal("/recap was held back by the turn count")
	}
	m.recap.busy, m.recap.show = false, false

	addTurn(m)
	m.unfocusedSince = time.Now().Add(-10 * time.Minute)
	if cmd := m.onFocus(); cmd == nil {
		t.Fatal("two new turns earned no recap")
	}
}

// A draft keeps an unasked recap away, but not one asked for: /recap runs
// while its own text is still in the composer.
func TestRecapDraftGatesOnlyUnaskedRecaps(t *testing.T) {
	m := newRecapModel(t)
	m.userInput.Textarea.SetValue("/recap")

	m.unfocusedSince = time.Now().Add(-10 * time.Minute)
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

	m.unfocusedSince = time.Now()
	if cmd := m.startPromptSuggestion(); cmd != nil || !m.suggestOwed {
		t.Fatal("a hint was fetched for an unfocused terminal")
	}
	if m.onFocus(); m.suggestOwed {
		t.Fatal("the hint skipped while away was not fetched on return")
	}
}
