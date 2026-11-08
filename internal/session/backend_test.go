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

var sessionBackends = []struct {
	name string
	open func(string) (*Store, error)
}{
	{"jsonl", NewStoreWithDir},
	{"sqlite", NewSQLiteStoreWithDir},
}

func backendOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestBackendSessionLifecycle(t *testing.T) {
	for _, backend := range sessionBackends {
		t.Run(backend.name, func(t *testing.T) {
			dir := t.TempDir()
			open := func() *Store {
				t.Helper()
				s, err := backend.open(dir)
				backendOK(t, err)
				t.Cleanup(func() { backendOK(t, s.Close()) })
				return s
			}
			a, b := open(), open()
			if missing, _, err := b.Open("missing"); err == nil {
				_ = missing.Close()
				t.Fatal("opened missing session")
			}
			writer, err := a.Create(CreateOptions{ID: "source", AgentID: "main"})
			backendOK(t, err)
			t.Cleanup(func() { _ = writer.Close() })
			wantPath := ""
			if backend.name == "jsonl" {
				wantPath = filepath.Join(dir, "transcripts", "source.jsonl")
			}
			if writer.TranscriptPath() != wantPath || a.TranscriptPath("source") != wantPath {
				t.Fatalf("unexpected transcript path: %q / %q", writer.TranscriptPath(), a.TranscriptPath("source"))
			}
			if wantPath != "" {
				_, err := os.Stat(wantPath)
				backendOK(t, err)
			}
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
			backendOK(t, writer.Err())
			backendOK(t, writer.Append(context.Background(), transcript.Record{Type: transcript.SessionStatePatched, State: &transcript.StatePatch{Title: new("custom"), Mode: new("plan")}}))
			backendOK(t, writer.Close())
			backendOK(t, a.Close())
			resumed, history, err := b.Open("source")
			backendOK(t, err)
			t.Cleanup(func() { _ = resumed.Close() })
			if !reflect.DeepEqual(history.Messages, messages) || history.Metadata.Title != "custom" || history.Metadata.Mode != "plan" {
				t.Fatalf("resume changed native history/state: %+v", history)
			}
			summary := ai.Message{ID: "summary", Role: ai.RoleUser, Content: ai.TextContent("summary")}
			resumed.RecordEvent(sdkagent.MessageAdded{Message: summary})
			backendOK(t, resumed.Append(context.Background(), transcript.Record{Type: transcript.SessionCompacted, Compaction: &transcript.Compaction{MessageID: "summary"}}))
			resumed.RecordEvent(sdkagent.MessagesReplaced{Messages: []ai.Message{summary}})
			backendOK(t, resumed.Err())
			backendOK(t, resumed.Close())
			backendOK(t, b.Close())
			c := open()
			final, history, err := c.Open("source")
			backendOK(t, err)
			t.Cleanup(func() { _ = final.Close() })
			if !reflect.DeepEqual(history.Messages, []ai.Message{summary}) {
				t.Fatalf("resume after compaction = %+v", history.Messages)
			}
			rows, err := c.ReadChatHistory("source")
			backendOK(t, err)
			if len(rows) != 3 {
				t.Fatalf("display history has %d rows, want 3", len(rows))
			}
			backendOK(t, final.Close())
			backendOK(t, c.Close())
		})
	}
}

func TestBackendForkOwnsAssetsAndConversation(t *testing.T) {
	for _, backend := range sessionBackends {
		t.Run(backend.name, func(t *testing.T) {
			store, err := backend.open(t.TempDir())
			backendOK(t, err)
			t.Cleanup(func() { backendOK(t, store.Close()) })
			source, err := store.Create(CreateOptions{ID: "source", AgentID: "main"})
			backendOK(t, err)
			t.Cleanup(func() { _ = source.Close() })
			backendOK(t, store.PersistToolResult("source", "call", "FULL OUTPUT"))
			backendOK(t, os.MkdirAll(store.ImagesDir("source"), 0700))
			imagePath := filepath.Join(store.ImagesDir("source"), "image.png")
			backendOK(t, os.WriteFile(imagePath, []byte("image bytes"), 0600))
			messages := []ai.Message{
				{ID: "image", Role: ai.RoleUser, Content: ai.TextContent("inspect " + imagePath)},
				{ID: "result", Role: ai.RoleUser, Content: ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: "call", Content: ai.TextContent("preview\n\n[Full output persisted to blobs/tool-result/source/call]")})}},
			}
			for _, m := range messages {
				source.RecordEvent(sdkagent.MessageAdded{Message: m})
			}
			backendOK(t, source.Err())
			backendOK(t, source.Append(context.Background(), transcript.Record{Type: transcript.SessionStatePatched, State: &transcript.StatePatch{Group: new("team"), Title: new("source title")}}))
			backendOK(t, source.Close())
			fork, history, err := store.Fork("source")
			backendOK(t, err)
			t.Cleanup(func() { _ = fork.Close() })
			if fork.ID() == "source" || history.Metadata.Group != "" {
				t.Fatalf("fork identity/group = %+v", history.Metadata)
			}
			forkImage := filepath.Join(store.ImagesDir(fork.ID()), "image.png")
			if !strings.Contains(history.Messages[0].Text(), forkImage) || strings.Contains(history.Messages[0].Text(), imagePath) {
				t.Fatalf("fork did not rewrite image path: %q", history.Messages[0].Text())
			}
			fork.RecordEvent(sdkagent.MessageAdded{Message: ai.Message{ID: "fork-only", Role: ai.RoleUser, Content: ai.TextContent("independent")}})
			backendOK(t, fork.Err())
			original, err := store.Read("source")
			backendOK(t, err)
			if !reflect.DeepEqual(original.Messages, messages) {
				t.Fatalf("fork changed source: %+v", original.Messages)
			}
			backendOK(t, store.Delete("source"))
			backendOK(t, fork.Close())
			reopened, history, err := store.Open(fork.ID())
			backendOK(t, err)
			t.Cleanup(func() { _ = reopened.Close() })
			if len(history.Messages) != 3 {
				t.Fatalf("fork lost conversation: %+v", history.Messages)
			}
			if got := store.ChatRows(history)[1].ToolResult.Text(); got != "FULL OUTPUT" {
				t.Fatalf("fork lost output: %q", got)
			}
			data, err := os.ReadFile(forkImage)
			backendOK(t, err)
			if string(data) != "image bytes" {
				t.Fatalf("fork image = %q", data)
			}
			if _, err := os.Stat(imagePath); !os.IsNotExist(err) {
				t.Fatalf("source assets remain: %v", err)
			}
			backendOK(t, reopened.Close())
		})
	}
}
