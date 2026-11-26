package input

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func selectorStore(t *testing.T) *session.Store {
	t.Helper()
	store, err := session.OpenStore(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func appendSelectorInput(t *testing.T, opened *session.Session) {
	t.Helper()
	if err := opened.AcceptInput(t.Context(), ai.Message{ID: "prompt", Role: ai.RoleUser, Content: ai.TextContent("Hello")}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestResumeViewportKeepsSelectedSessionAndActionsVisible(t *testing.T) {
	for _, height := range []int{12, 24} {
		s := NewSessionSelector()
		s.active = true
		s.Resize(80, height)
		for i := range 12 {
			id := fmt.Sprintf("notes-%02d", i)
			s.sessions = append(s.sessions, session.ListItem{Overview: session.Overview{SessionID: id, Title: id, LastPrompt: "Review the notes"}})
		}
		s.updateFilter()
		for range 6 {
			s.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
		}
		view := s.Render()
		if lines := strings.Count(view, "\n") + 1; lines > height || !strings.Contains(view, "notes-06") || !strings.Contains(view, "Enter select") || !strings.Contains(view, "Review the notes") {
			t.Fatalf("height %d rendered %d lines:\n%s", height, lines, view)
		}
		if got := s.Select()().(sessionSelectedMsg).SessionID; got != "notes-06" {
			t.Fatalf("selected %s", got)
		}
	}
}

func TestResumeProjectTitleKeepsMatchCount(t *testing.T) {
	s := NewSessionSelector()
	s.active = true
	s.cwd = "/workspace/" + strings.Repeat("项目", 9)
	s.sessions = []session.ListItem{{Overview: session.Overview{Title: "first"}}, {Overview: session.Overview{Title: "second"}}}
	s.HandleKeypress(tea.KeyPressMsg{Code: 'f', Text: "first"})
	for _, width := range []int{40, 100} {
		s.Resize(width, 24)
		title := strings.Split(ansi.Strip(s.Render()), "\n")[0]
		if ansi.StringWidth(title) > width || !strings.HasPrefix(title, "Resume Session") || !strings.HasSuffix(title, " (1/2)") || (width == 100 && !strings.Contains(title, strings.Repeat("项目", 9))) {
			t.Errorf("width %d: %s", width, title)
		}
	}
}

func TestResumeFilterKeepsCursorOnRow(t *testing.T) {
	s := NewSessionSelector()
	s.active = true
	s.Resize(40, 24)
	query := "project-" + strings.Repeat("界", 20) + "notes"
	s.sessions = []session.ListItem{{Overview: session.Overview{SessionID: "wanted", Title: query}}, {Overview: session.Overview{SessionID: "other", Title: "notes"}}}
	s.HandleKeypress(tea.KeyPressMsg{Code: 'p', Text: query})
	line := strings.Split(ansi.Strip(s.Render()), "\n")[1]
	if !strings.HasSuffix(line, "notes▏") || ansi.StringWidth(line) > 40 {
		t.Fatalf("filter row width %d: %q", ansi.StringWidth(line), line)
	}
	s.Resize(80, 24)
	if !strings.Contains(ansi.Strip(s.Render()), query+"▏") || len(s.filtered) != 1 || s.filtered[0].SessionID != "wanted" {
		t.Fatal("render truncation changed the filter")
	}
}

func TestResumeNoMatchActions(t *testing.T) {
	s := NewSessionSelector()
	s.active = true
	s.Resize(40, 24)
	s.sessions = []session.ListItem{{Overview: session.Overview{SessionID: "one", Title: "notes"}}}
	s.updateFilter()
	s.HandleKeypress(tea.KeyPressMsg{Code: 'z', Text: "z"})
	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	if got := s.Render(); strings.Contains(got, "Enter select") || strings.Contains(got, "navigate") || !strings.Contains(got, "Esc clear") || s.HandleKeypress(enter) != nil {
		t.Fatalf("empty filter actions: %q", got)
	}
	s.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEsc})
	if !strings.Contains(s.Render(), "Enter select") {
		t.Fatal("selection hint not restored")
	}
	cmd := s.HandleKeypress(enter)
	if cmd == nil || cmd().(sessionSelectedMsg).SessionID != "one" {
		t.Fatal("session not selected")
	}
}

func TestResumeUnicodeFilterSelectsMatchingSession(t *testing.T) {
	s := NewSessionSelector()
	s.sessions = []session.ListItem{
		{Overview: session.Overview{SessionID: "other", Title: "crème © notes"}},
		{Overview: session.Overview{SessionID: "wanted", Title: "résumé notes"}},
	}
	s.HandleKeypress(tea.KeyPressMsg{Code: 'é', Text: "én"})
	cmd := s.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("ordered fuzzy query selected no session")
	}
	if msg := cmd().(sessionSelectedMsg); msg.SessionID != "wanted" {
		t.Fatalf("selected %q, want wanted", msg.SessionID)
	}
}

func TestSessionSelectorLoadsOffLoopAndKeepsTypedFilter(t *testing.T) {
	store := selectorStore(t)
	opened, err := store.Create(t.Context(), session.CreateOptions{ID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	appendSelectorInput(t, opened)
	state := SessionState{Selector: NewSessionSelector()}
	cmd, err := state.Selector.EnterSelect(80, 24, store, "/workspace")
	if err != nil || cmd == nil {
		t.Fatalf("EnterSelect: %v", err)
	}
	if !strings.Contains(state.Selector.Render(), "Loading sessions") || state.Selector.Select() != nil {
		t.Fatal("selector must paint before disk I/O and cannot select unloaded data")
	}
	state.Selector.HandleKeypress(tea.KeyPressMsg{Code: 'z', Text: "zzzz"})
	UpdateSession(OverlayDeps{}, &state, cmd())
	if state.Selector.loading || len(state.Selector.sessions) != 1 || len(state.Selector.filtered) != 0 || state.Selector.nav.Search != "zzzz" {
		t.Fatal("loading discarded the user's filter")
	}
	state.Selector.HandleKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
	if got := state.Selector.Render(); !strings.Contains(got, "1 msg ·") {
		t.Fatalf("single-message row: %q", got)
	}
	if !state.Selector.IsActive() || state.Selector.Select() == nil {
		t.Fatal("clearing the filter must restore selection")
	}
	state.Selector.Resize(50, 12)
	if state.Selector.nav.MaxVisible != 1 || state.Selector.width != 50 {
		t.Fatal("selector retained its opening dimensions")
	}
}

func TestSessionSelectorCancelRejectsStaleResultsAfterReopen(t *testing.T) {
	store := selectorStore(t)
	state := SessionState{Selector: NewSessionSelector()}
	old, err := state.Selector.EnterSelect(80, 24, store, "")
	if err != nil {
		t.Fatal(err)
	}
	state.Selector.Cancel()
	cancelled := old().(sessionsListedMsg)
	if !errors.Is(cancelled.err, context.Canceled) {
		t.Fatalf("cancel did not stop listing: %v", cancelled.err)
	}
	current, err := state.Selector.EnterSelect(80, 24, store, "")
	if err != nil {
		t.Fatal(err)
	}
	defer state.Selector.Cancel()
	UpdateSession(OverlayDeps{}, &state, sessionsListedMsg{generation: cancelled.generation, sessions: []session.ListItem{{Overview: session.Overview{SessionID: "stale"}}}})
	if !state.Selector.loading || len(state.Selector.sessions) != 0 {
		t.Fatal("an old result replaced the reopened selector")
	}
	UpdateSession(OverlayDeps{}, &state, current())
	if !strings.Contains(state.Selector.Render(), "No sessions found") {
		t.Fatal("empty store did not finish loading")
	}
}

func TestSessionSelectorRendersCachedTitlesAndPreviews(t *testing.T) {
	state := SessionState{Selector: NewSessionSelector()}
	cmd, err := state.Selector.EnterSelect(80, 24, selectorStore(t), "")
	if err != nil {
		t.Fatal(err)
	}
	defer state.Selector.Cancel()
	msg := cmd().(sessionsListedMsg)
	msg.sessions = []session.ListItem{{Overview: session.Overview{SessionID: "unreadable", Title: "Generated title", LastPrompt: "缓存中的预览\nsecond line"}, Attrs: session.Attrs{Title: "Go"}}}
	UpdateSession(OverlayDeps{}, &state, msg)
	view := state.Selector.Render()
	state.Selector.nav.Search = "go"
	state.Selector.updateFilter()
	if len(state.Selector.filtered) != 1 {
		t.Fatal("filter ignored the explicit title")
	}
	if !strings.Contains(view, "Go") || !strings.Contains(view, "缓存中的预览") || strings.Contains(view, "second line") {
		t.Fatal("render must use cached metadata, including a short explicit title")
	}
}

func TestSessionSelectorShowsListErrors(t *testing.T) {
	state := SessionState{Selector: NewSessionSelector()}
	cmd, err := state.Selector.EnterSelect(80, 24, selectorStore(t), "")
	if err != nil {
		t.Fatal(err)
	}
	defer state.Selector.Cancel()
	msg := cmd().(sessionsListedMsg)
	msg.err = errors.New("listing failed")
	UpdateSession(OverlayDeps{}, &state, msg)
	if state.Selector.loading || !strings.Contains(state.Selector.Render(), "listing failed") {
		t.Fatal("listing error remained a loading screen")
	}
}

func TestSessionSelectorListsAllPagesAndOnlyNonemptyRoots(t *testing.T) {
	store := selectorStore(t)
	for i := range 203 {
		opts := session.CreateOptions{ID: fmt.Sprintf("session-%03d", i)}
		if i == 201 {
			opts.Parent = &session.ParentLink{Session: "session-000"}
		}
		opened, err := store.Create(t.Context(), opts)
		if err != nil {
			t.Fatal(err)
		}
		if i != 202 {
			appendSelectorInput(t, opened)
		}
		if err := opened.Close(); err != nil {
			t.Fatal(err)
		}
	}
	state := SessionState{Selector: NewSessionSelector()}
	cmd, err := state.Selector.EnterSelect(80, 24, store, "")
	if err != nil {
		t.Fatal(err)
	}
	defer state.Selector.Cancel()
	UpdateSession(OverlayDeps{}, &state, cmd())
	if state.Selector.loadError != nil || len(state.Selector.sessions) != 201 {
		t.Fatalf("sessions=%d error=%v", len(state.Selector.sessions), state.Selector.loadError)
	}
	for _, item := range state.Selector.sessions {
		if item.Parent != "" {
			t.Fatal("worker offered as main conversation")
		}
	}
}

func BenchmarkSessionSelectorFilterAndRender(b *testing.B) {
	s := NewSessionSelector()
	s.active = true
	s.Resize(120, 40)
	for i := range 1000 {
		s.sessions = append(s.sessions, session.ListItem{Overview: session.Overview{SessionID: string(rune(i)), Title: "Diagnostic session title", LastPrompt: "Cached prompt preview"}})
	}
	b.ReportAllocs()
	for b.Loop() {
		s.updateFilter()
		_ = s.Render()
	}
}
