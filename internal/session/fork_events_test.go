package session

import (
	"context"
	"os"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/transcript"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestReturningToSessionKeepsEarlierMessages(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	original := mustSession(t, store.transcriptStore, CreateOptions{ID: "original"})
	original.RecordEvent(sdkagent.MessageAdded{Message: core.Message{ID: "old", Role: ai.RoleUser, Content: ai.TextContent("old conversation")}})
	if err := original.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := store.Create(CreateOptions{ID: "other"})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	reopened, history, err := store.Open("original")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if len(history.Messages) != 1 {
		t.Fatal(history)
	}
	reopened.RecordEvent(sdkagent.MessageAdded{Message: core.Message{ID: "new", Role: ai.RoleUser, Content: ai.TextContent("new turn")}})
	got, err := store.Read("original")
	if err != nil || len(got.Messages) != 2 || got.Messages[1].ID != "new" {
		t.Fatalf("history: %+v, %v", got, err)
	}
}
func TestForkSessionWritesToItsOwnTranscript(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	original := mustSession(t, store.transcriptStore, CreateOptions{ID: "parent"})
	msg := core.Message{ID: "before", Role: ai.RoleUser, Content: ai.TextContent("before")}
	if err := original.Append(context.Background(), transcript.Record{Type: transcript.MessageAppended, Message: &msg}); err != nil {
		t.Fatal(err)
	}
	forkSession, fork, err := store.Fork("parent")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = forkSession.Close() })
	opened := forkSession
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	opened.RecordEvent(sdkagent.MessageAdded{Message: core.Message{ID: "after", Role: ai.RoleUser, Content: ai.TextContent("after")}})
	parent, err := store.Read("parent")
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.Read(fork.Metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(parent.Messages) != 1 || len(child.Messages) != 2 || child.Messages[1].ID != "after" {
		t.Fatalf("parent=%+v child=%+v", parent.Messages, child.Messages)
	}
}
func readTranscript(t *testing.T, store *Store, sessionID string) string {
	t.Helper()
	data, err := os.ReadFile(store.transcriptStore.TranscriptPath(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read transcript %s: %v", sessionID, err)
	}
	return string(data)
}
