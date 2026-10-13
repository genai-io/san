package app

import (
	"path/filepath"
	"testing"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestClearStartsAFreshDurableSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SAN_TASK_LIST_ID", "")
	store, err := session.NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	active := session.NewActive(store, "old")
	defer active.Close()
	old := active.NewWriter("main", "test", "model", 100)
	history := []ai.Message{{ID: "old-message", Role: ai.RoleUser, Content: ai.TextContent("old text")}}
	old.OnAgentEvent(sdkagent.MessageAdded{Message: history[0]})
	m := model{services: services{Agent: &agent.Runtime{}, Session: active, Tracker: todo.NewStore(), Task: task.NewManager(), Reminder: reminder.NewService(), Hook: hook.NewEngine(nil, "old", "", active.TranscriptPath())}, conv: conv.NewModel(80)}
	m.services.Agent.SetMessages(history)
	m.env.SessionName = "old title"
	if err := m.clearSession(); err != nil {
		t.Fatal(err)
	}
	id := active.ID()
	if id == "old" || id == "" {
		t.Fatalf("clear kept old identity: %q", id)
	}
	if m.env.SessionName != "" || len(m.services.Agent.Messages()) != 0 {
		t.Fatal("clear retained old session state")
	}
	if filepath.Base(m.services.Tracker.StorageDir()) != id {
		t.Fatal("task storage kept old identity")
	}
	// Delayed events from the retired runtime must not write into either log.
	old.OnAgentEvent(sdkagent.MessageAdded{Message: ai.Message{ID: "late", Role: ai.RoleUser, Content: ai.TextContent("late")}})
	w := active.NewWriter("main", "test", "model", 100)
	w.OnAgentEvent(sdkagent.MessageAdded{Message: ai.Message{ID: "new-message", Role: ai.RoleUser, Content: ai.TextContent("new text")}})
	for sessionID, want := range map[string]string{"old": "old-message", id: "new-message"} {
		got, err := store.Load(sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Messages) != 1 || got.Messages[0].ID != want {
			t.Fatalf("session %s leaked messages: %+v", sessionID, got.Messages)
		}
	}
}

func initClearSessionTest(t *testing.T, m *model) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SAN_TASK_LIST_ID", "")
	m.services.Session = session.NewActive(nil, "before-clear")
	m.services.Hook = hook.NewEngine(nil, "before-clear", "", "")
	m.services.Task = task.NewManager()
}
