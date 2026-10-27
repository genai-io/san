package input

import (
	"context"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/session"
)

func TestResumeCommandListsInjectedStore(t *testing.T) {
	store, err := session.NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opened, err := store.Create(session.CreateOptions{ID: "existing"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	m := &Model{Session: SessionState{Selector: NewSessionSelector()}}
	c := NewSlashCommandController(SlashCommandEnv{
		Input: m, SessionStore: store, Width: 80, Height: 24,
	})
	result, cmd, handled := c.Execute(context.Background(), "/resume")
	if !handled || result != "" || cmd == nil || !m.Session.Selector.IsActive() {
		t.Fatalf("resume did not open the selector: handled=%v result=%q", handled, result)
	}
	UpdateSession(OverlayDeps{}, &m.Session, cmd())
	selected := m.Session.Selector.Select()
	if selected == nil {
		t.Fatal("resume did not list the stored session")
	}
	if msg, ok := selected().(sessionSelectedMsg); !ok || msg.SessionID != opened.ID() {
		t.Fatalf("selected session = %+v, want %s", msg, opened.ID())
	}
}

func TestResumeCommandReportsMissingStore(t *testing.T) {
	m := &Model{Session: SessionState{Selector: NewSessionSelector()}}
	c := NewSlashCommandController(SlashCommandEnv{Input: m, Width: 80, Height: 24})
	result, cmd, handled := c.Execute(context.Background(), "/resume")
	if !handled || cmd != nil || !strings.Contains(result, "session store is required") {
		t.Fatalf("missing store was not reported: handled=%v result=%q", handled, result)
	}
	if m.Session.Selector.IsActive() {
		t.Fatal("resume activated the selector without a store")
	}
}
