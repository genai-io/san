package tui

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/runtime/reminder"
	"github.com/genai-io/san/internal/setting"

	"github.com/genai-io/san/internal/app/tui/chat"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/infra/filecache"
	agentruntime "github.com/genai-io/san/internal/runtime"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/san/tests/integration/testutil"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestClearStartsAFreshDraft(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SAN_TASK_LIST_ID", "")
	cwd := t.TempDir()
	store := testutil.OpenSessionStore(t, cwd)
	old := mustCreateSession(t, store, session.CreateOptions{ID: "old", AgentID: "main"})
	history := []ai.Message{{ID: "old-message", Role: ai.RoleUser, Content: ai.TextContent("old text")}}
	if err := testutil.AppendMessages(old, history); err != nil {
		t.Fatal(err)
	}
	m := model{features: appFeatures{Agent: &agentruntime.Runtime{}, Session: old, SessionStore: store, Tracker: todo.NewStore(), Task: task.NewRunSet(), Reminder: reminder.NewQueue(), Hook: hook.NewEngine(nil, "old", "", old.Path())}, conv: chat.NewModel(80)}
	m.features.Setting = setting.New(setting.NewData())
	m.features.LLM = &llm.Conn{}
	m.autopilot = new(atomic.Pointer[autopilotSnapshot])
	m.features.Agent.SetMessages(history)
	m.env.CWD = cwd
	m.env.SessionName = "old title"
	m.env.FileCache = filecache.New()
	if err := m.clearSession(); err != nil {
		t.Fatal(err)
	}
	id := m.sessionID()
	if id == "old" || id == "" {
		t.Fatalf("clear kept old identity: %q", id)
	}
	if m.env.SessionName != "" || len(m.features.Agent.Messages()) != 0 {
		t.Fatal("clear retained old session state")
	}
	if filepath.Base(m.features.Tracker.StorageDir()) != id {
		t.Fatal("task storage kept old identity")
	}
	if _, err := os.Stat(m.features.Session.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("/clear saved an empty transcript: %v", err)
	}
	// Delayed events from the retired runtime must not write into either log.
	if err := testutil.AppendMessages(old, []ai.Message{{ID: "late", Role: ai.RoleUser, Content: ai.TextContent("late")}}); err == nil {
		t.Fatal("retired writer accepted late input")
	}
	w, err := m.openCurrentSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := testutil.AppendMessages(w, []ai.Message{{ID: "new-message", Role: ai.RoleUser, Content: ai.TextContent("new text")}}); err != nil {
		t.Fatal(err)
	}
	for sessionID, want := range map[string]string{"old": "old-message", id: "new-message"} {
		got, err := store.Read(t.Context(), sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Messages()) != 1 || got.Messages()[0].ID != want {
			t.Fatalf("session %s leaked messages: %+v", sessionID, got.Messages())
		}
	}
}

func initClearSessionTest(t *testing.T, m *model) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SAN_TASK_LIST_ID", "")
	m.env.CWD = t.TempDir()
	m.features.Session = nil
	m.features.sessionID = "before-clear"
	m.features.Hook = hook.NewEngine(nil, "before-clear", "", "")
	m.features.Task = task.NewRunSet()
	m.env.FileCache = filecache.New()
}

func mustCreateSession(t *testing.T, store *session.Store, opts session.CreateOptions) *session.Session {
	t.Helper()
	opened, err := store.Create(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	return opened
}
