package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/transcript"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	sdksession "github.com/genai-io/sdk-go/pkg/session"
)

func requireStoreOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestJSONLSessionLifecycle(t *testing.T) {
	dir := t.TempDir()
	open := func() *Store {
		t.Helper()
		s, err := NewStoreWithDir(dir)
		requireStoreOK(t, err)
		t.Cleanup(func() { requireStoreOK(t, s.Close()) })
		return s
	}
	a, b := open(), open()
	if missing, _, err := b.Open("missing"); err == nil {
		_ = missing.Close()
		t.Fatal("opened missing session")
	}
	writer, err := a.Create(CreateOptions{ID: "source", AgentID: "main"})
	requireStoreOK(t, err)
	t.Cleanup(func() { _ = writer.Close() })
	wantPath := filepath.Join(dir, "transcripts", "source.jsonl")
	if writer.TranscriptPath() != wantPath || a.TranscriptPath("source") != wantPath {
		t.Fatalf("unexpected transcript path: %q / %q", writer.TranscriptPath(), a.TranscriptPath("source"))
	}
	_, err = os.Stat(wantPath)
	requireStoreOK(t, err)
	if duplicate, err := b.Create(CreateOptions{ID: "source"}); err == nil {
		_ = duplicate.Close()
		t.Fatal("duplicate identity accepted")
	}
	if other, _, err := b.Open("source"); !errors.Is(err, sdksession.ErrBusy) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatalf("open claimed = %v", err)
	}
	if err := b.Delete("source"); !errors.Is(err, sdksession.ErrBusy) {
		t.Fatalf("delete claimed = %v", err)
	}
	if err := a.Close(); !errors.Is(err, sdksession.ErrBusy) {
		t.Fatalf("close claimed = %v", err)
	}
	messages := []ai.Message{
		{ID: "user", Role: ai.RoleUser, Content: ai.Content{ai.TextBlock("hello"), ai.ImageBlock(ai.Image{MediaType: "image/png", Data: "aGVsbG8="})}},
		{ID: "answer", Role: ai.RoleAssistant, Content: ai.Content{ai.ThinkingBlock("thought", "signature"), ai.TextBlock("answer")}},
	}
	for _, m := range messages {
		writer.RecordEvent(sdkagent.MessageAdded{Message: m})
	}
	requireStoreOK(t, writer.Err())
	requireStoreOK(t, writer.Append(context.Background(), transcript.Record{Type: transcript.SessionStatePatched, State: &transcript.StatePatch{Title: new("custom"), Mode: new("plan")}}))
	requireStoreOK(t, writer.Close())
	requireStoreOK(t, a.Close())
	resumed, history, err := b.Open("source")
	requireStoreOK(t, err)
	t.Cleanup(func() { _ = resumed.Close() })
	if !reflect.DeepEqual(history.Messages, messages) || history.Metadata.Title != "custom" || history.Metadata.Mode != "plan" {
		t.Fatalf("resume changed native history/state: %+v", history)
	}
	summary := ai.Message{ID: "summary", Role: ai.RoleUser, Content: ai.TextContent("summary")}
	resumed.RecordEvent(sdkagent.MessageAdded{Message: summary})
	requireStoreOK(t, resumed.Append(context.Background(), transcript.Record{Type: transcript.SessionCompacted, Compaction: &transcript.Compaction{MessageID: "summary"}}))
	resumed.RecordEvent(sdkagent.MessagesReplaced{Messages: []ai.Message{summary}})
	requireStoreOK(t, resumed.Err())
	requireStoreOK(t, resumed.Close())
	requireStoreOK(t, b.Close())
	c := open()
	final, history, err := c.Open("source")
	requireStoreOK(t, err)
	t.Cleanup(func() { _ = final.Close() })
	if !reflect.DeepEqual(history.Messages, []ai.Message{summary}) {
		t.Fatalf("resume after compaction = %+v", history.Messages)
	}
	full, err := c.Read("source", ReadOptions{FullHistory: true})
	requireStoreOK(t, err)
	rows := c.ChatRows(full)
	if len(rows) != 3 {
		t.Fatalf("display history has %d rows, want 3", len(rows))
	}
	requireStoreOK(t, final.Close())
	requireStoreOK(t, c.Close())
}

func TestJSONLForkOwnsAssetsAndConversation(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	requireStoreOK(t, err)
	t.Cleanup(func() { requireStoreOK(t, store.Close()) })
	source, err := store.Create(CreateOptions{ID: "source", AgentID: "main"})
	requireStoreOK(t, err)
	t.Cleanup(func() { _ = source.Close() })
	requireStoreOK(t, store.PersistToolResult("source", "call", "FULL OUTPUT"))
	requireStoreOK(t, os.MkdirAll(store.ImagesDir("source"), 0700))
	imagePath := filepath.Join(store.ImagesDir("source"), "image.png")
	requireStoreOK(t, os.WriteFile(imagePath, []byte("image bytes"), 0600))
	messages := []ai.Message{
		{ID: "image", Role: ai.RoleUser, Content: ai.TextContent("inspect " + imagePath)},
		{ID: "result", Role: ai.RoleUser, Content: ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: "call", Content: ai.TextContent("preview\n\n[Full output persisted to blobs/tool-result/source/call]")})}},
	}
	for _, m := range messages {
		source.RecordEvent(sdkagent.MessageAdded{Message: m})
	}
	requireStoreOK(t, source.Err())
	requireStoreOK(t, source.Append(context.Background(), transcript.Record{Type: transcript.SessionStatePatched, State: &transcript.StatePatch{Group: new("team"), Title: new("source title")}}))
	requireStoreOK(t, source.Close())
	fork, history, err := store.Fork("source")
	requireStoreOK(t, err)
	t.Cleanup(func() { _ = fork.Close() })
	if fork.ID() == "source" || history.Metadata.Group != "" {
		t.Fatalf("fork identity/group = %+v", history.Metadata)
	}
	forkImage := filepath.Join(store.ImagesDir(fork.ID()), "image.png")
	if !strings.Contains(history.Messages[0].Text(), forkImage) || strings.Contains(history.Messages[0].Text(), imagePath) {
		t.Fatalf("fork did not rewrite image path: %q", history.Messages[0].Text())
	}
	fork.RecordEvent(sdkagent.MessageAdded{Message: ai.Message{ID: "fork-only", Role: ai.RoleUser, Content: ai.TextContent("independent")}})
	requireStoreOK(t, fork.Err())
	original, err := store.Read("source", ReadOptions{})
	requireStoreOK(t, err)
	if !reflect.DeepEqual(original.Messages, messages) {
		t.Fatalf("fork changed source: %+v", original.Messages)
	}
	requireStoreOK(t, store.Delete("source"))
	requireStoreOK(t, fork.Close())
	reopened, history, err := store.Open(fork.ID())
	requireStoreOK(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	if len(history.Messages) != 3 {
		t.Fatalf("fork lost conversation: %+v", history.Messages)
	}
	if got := store.ChatRows(history)[1].ToolResult.Text(); got != "FULL OUTPUT" {
		t.Fatalf("fork lost output: %q", got)
	}
	data, err := os.ReadFile(forkImage)
	requireStoreOK(t, err)
	if string(data) != "image bytes" {
		t.Fatalf("fork image = %q", data)
	}
	if _, err := os.Stat(imagePath); !os.IsNotExist(err) {
		t.Fatalf("source assets remain: %v", err)
	}
	requireStoreOK(t, reopened.Close())
}
