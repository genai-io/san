package session

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/san/internal/transcript"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func mustSession(t *testing.T, store *transcript.Store, opts CreateOptions) *Session {
	t.Helper()
	opened, err := newSession(store, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	return opened
}

func TestOpenHoldsTheLeaseBeforeRestoringHistory(t *testing.T) {
	dir := t.TempDir()
	a, err := NewStoreWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewStoreWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	owner := mustSession(t, a.transcriptStore, CreateOptions{ID: "s"})
	appendMessage := func(opened *Session, id string) {
		msg := ai.Message{ID: id, Role: ai.RoleUser, Content: ai.TextContent(id)}
		if err := opened.Append(context.Background(), transcript.Record{Type: transcript.MessageAppended, Message: &msg}); err != nil {
			t.Fatal(err)
		}
	}
	appendMessage(owner, "m1")
	if _, err := b.Read("s"); err != nil {
		t.Fatal(err)
	}
	if opened, _, err := b.Open("s"); err == nil {
		_ = opened.Close()
		t.Fatal("busy session opened")
	}
	appendMessage(owner, "m2")
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	opened, history, err := b.Open("s")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	if len(history.Messages) != 2 || history.Messages[1].ID != "m2" {
		t.Fatalf("stale history: %+v", history.Messages)
	}
	if competing, _, err := a.Open("s"); err == nil {
		_ = competing.Close()
		t.Fatal("idle opened session released its lease")
	}
	opened.RecordEvent(sdkagent.MessagesReplaced{Messages: history.Messages})
	appendMessage(opened, "m3")
	got, err := b.Read("s")
	if err != nil || len(got.Messages) != 3 {
		t.Fatalf("history lost: %+v, %v", got, err)
	}
}

func TestOpeningMissingSessionDoesNotCreateATranscript(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if opened, _, err := store.Open("missing"); err == nil {
		_ = opened.Close()
		t.Fatal("missing session opened")
	}
	if items, err := store.List(); err != nil || len(items) != 0 {
		t.Fatalf("phantom session: %+v, %v", items, err)
	}
}

func TestClosedSessionRejectsMessagesAndState(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opened, err := store.Create(CreateOptions{ID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	before := readTranscript(t, store, "s")
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
	if err := opened.Append(context.Background(), transcript.Record{Type: transcript.SessionStatePatched, State: &transcript.StateRecord{Title: new("late")}}); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed state append: %v", err)
	}
	opened.RecordEvent(sdkagent.MessageAdded{Message: ai.UserMessage("late")})
	if after := readTranscript(t, store, "s"); after != before {
		t.Fatal("closed observer wrote a record")
	}
	next, _, err := store.Open("s")
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
	if competing, _, err := store.Open("s"); err == nil {
		_ = competing.Close()
		t.Fatal("repeated Close released a newer lease")
	}
}
func TestHydratedResumeDoesNotRewriteNativeHistory(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const preview = "preview\n\n[Full output persisted to blobs/tool-result/s/c]"
	snapshot := &History{Metadata: Metadata{ID: "s"}, Messages: []ai.Message{
		{ID: "call", Role: ai.RoleAssistant, Content: ai.Content{ai.ToolCallBlock(ai.ToolCall{ID: "c", Name: "Bash", Input: `{}`})}},
		{ID: "result", Role: ai.RoleUser, Content: ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: "c", ToolName: "Bash", Content: ai.TextContent(preview)})}},
	}}
	if err := store.importHistory(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := store.PersistToolResult("s", "c", "full output"); err != nil {
		t.Fatal(err)
	}
	opened, resumed, err := store.Open("s")
	defer func() { _ = opened.Close() }()
	if err != nil {
		t.Fatal(err)
	}
	if got := store.ChatRows(resumed)[1].ToolResult.Text(); got != "full output" {
		t.Fatalf("display did not hydrate the blob: %q", got)
	}
	if got := resumed.Messages[1].ToolResults()[0].Text(); got != preview {
		t.Fatalf("resume changed native preview: %q", got)
	}
	before := readTranscript(t, store, "s")
	opened.RecordEvent(sdkagent.MessagesReplaced{Messages: resumed.Messages})
	if after := readTranscript(t, store, "s"); before != after {
		t.Fatal("hydrated resume copied or replaced the canonical transcript")
	}
}

// importHistory seeds a transcript through the opened session in unit tests.
func (s *Store) importHistory(history *History) error {
	if history == nil {
		return fmt.Errorf("history is nil")
	}
	normalizeMetadata(&history.Metadata, history.Messages, s.cwd, time.Now())
	meta := history.Metadata
	opened, err := newSession(s.transcriptStore, CreateOptions{ID: meta.ID, CreatedAt: meta.CreatedAt,
		Cwd: meta.Cwd, Provider: meta.Provider, Model: meta.Model, ParentSessionID: meta.ParentSessionID})
	if err != nil {
		return err
	}
	defer func() { _ = opened.Close() }()
	for i := range history.Messages {
		if history.Messages[i].ID == "" {
			history.Messages[i].ID = core.NewMessageID()
		}
	}
	if err := opened.Append(context.Background(), transcript.Record{Type: transcript.ConversationReplaced, Messages: history.Messages}); err != nil {
		return err
	}
	return opened.Append(context.Background(), transcript.Record{Type: transcript.SessionStatePatched,
		GitBranch: getGitBranch(s.cwd), State: &transcript.StateRecord{Title: new(meta.Title), LastPrompt: new(meta.LastPrompt), Mode: new(meta.Mode), AutoPilot: new(meta.AutoPilot), Group: new(meta.Group), Tasks: new(history.Tasks)}})
}

func normalizeMetadata(meta *Metadata, msgs []core.Message, defaultCwd string, now time.Time) {
	if meta.ID == "" {
		meta.ID = NewSessionID()
	}
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = now
	}
	meta.UpdatedAt = now
	meta.MessageCount = len(msgs)
	if meta.Cwd == "" {
		meta.Cwd = defaultCwd
	}
	if meta.LastPrompt == "" {
		meta.LastPrompt = ExtractLastUserText(msgs)
	}
	if meta.Title == "" {
		meta.Title = GenerateTitle(msgs)
	}
}

func TestRepeatedMessageDoesNotMoveTheParentPosition(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opened, err := store.Create(CreateOptions{ID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	first := core.Message{ID: "m1", Role: "user", Content: ai.TextContent("first")}
	second := core.Message{ID: "m2", Role: "assistant", Content: ai.TextContent("second")}
	for _, msg := range []core.Message{first, second, first, {ID: "m3", Role: "user", Content: ai.TextContent("third")}} {
		if err := opened.Append(context.Background(), transcript.Record{Type: transcript.MessageAppended, Message: &msg}); err != nil {
			t.Fatal(err)
		}
	}
	opened.RecordEvent(sdkagent.MessageAdded{Message: first})
	if err := opened.Append(context.Background(), transcript.Record{Type: transcript.NoticeAdded, Notice: &transcript.NoticeRecord{Text: "notice"}}); err != nil {
		t.Fatal(err)
	}
	for _, record := range readAllRecords(t, store.projectDir, "s") {
		if record.Message != nil && record.Message.ID == "m3" && record.ParentMessageID != "m2" {
			t.Fatalf("third message parent=%s", record.ParentMessageID)
		}
		if record.Notice != nil && record.Notice.AfterMessageID != "m3" {
			t.Fatalf("notice position=%s", record.Notice.AfterMessageID)
		}
	}
	got, err := store.Read("s")
	if err != nil || len(got.Messages) != 3 {
		t.Fatalf("history=%+v, %v", got, err)
	}
}

func TestRepeatedStateRecordsSkipUnchangedFieldsAndClearTasks(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opened, err := store.Create(CreateOptions{ID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	record := transcript.Record{Type: transcript.SessionStatePatched, State: &transcript.StateRecord{Title: new("title"), Group: new("group"), Tasks: new([]todo.Item{{ID: "1", Subject: "work", Metadata: map[string]any{"worker": "original"}}})}}
	for range 3 {
		if err := opened.Append(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	before := len(readAllRecords(t, store.projectDir, "s"))
	history, err := store.Read("s")
	if err != nil {
		t.Fatal(err)
	}
	history.Tasks[0].Metadata["worker"] = "changed by reader"
	if err := opened.Append(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if got := len(readAllRecords(t, store.projectDir, "s")); got != before {
		t.Fatalf("unchanged state duplicated: %d -> %d", before, got)
	}
	if err := opened.Append(context.Background(), transcript.Record{Type: transcript.SessionStatePatched, State: &transcript.StateRecord{Tasks: new([]todo.Item{}), Group: new("")}}); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Read("s")
	if err != nil || len(restored.Tasks) != 0 || restored.Metadata.Group != "" {
		t.Fatalf("state not cleared: %+v, %v", restored, err)
	}
}
