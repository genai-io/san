package input

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/session"
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

func TestSessionSelectorLoadsOffLoopAndKeepsTypedFilter(t *testing.T) {
	store := selectorStore(t)
	opened, err := store.Create(t.Context(), session.CreateOptions{ID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
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
	if !state.Selector.IsActive() || state.Selector.Select() == nil {
		t.Fatal("clearing the filter must restore selection")
	}
	state.Selector.Resize(50, 12)
	if state.Selector.nav.MaxVisible != 3 || state.Selector.width != 50 {
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

func TestSessionSelectorListsAllPagesAndOnlyRoots(t *testing.T) {
	store := selectorStore(t)
	for i := range 202 {
		opts := session.CreateOptions{ID: fmt.Sprintf("session-%03d", i)}
		if i == 201 {
			opts.Parent = &session.ParentLink{Session: "session-000"}
		}
		opened, err := store.Create(t.Context(), opts)
		if err != nil {
			t.Fatal(err)
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
