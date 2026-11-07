package transcript

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai"
	sdksession "github.com/genai-io/sdk-go/pkg/session"
)

var transcriptBackends = []struct {
	name string
	open func(string, string) (*Store, error)
}{
	{"jsonl", NewStore},
	{"sqlite", NewSQLiteStore},
}

func TestBackendReplayAndSummaries(t *testing.T) {
	for _, backend := range transcriptBackends {
		t.Run(backend.name, func(t *testing.T) {
			dir := t.TempDir()
			open := func() *Store {
				t.Helper()
				s, err := backend.open(dir, "p")
				mustOK(t, "open", err)
				t.Cleanup(func() { mustOK(t, "close", s.Close()) })
				return s
			}
			ctx := context.Background()
			a := open()
			mustOK(t, "start", a.Start(ctx, StartCommand{SessionID: "s", Time: time.Now()}))
			wantPath := ""
			if backend.name == "jsonl" {
				wantPath = filepath.Join(dir, "transcripts", "s.jsonl")
			}
			if got := a.TranscriptPath("s"); got != wantPath {
				t.Fatalf("path = %q, want %q", got, wantPath)
			}
			if wantPath != "" {
				_, err := os.Stat(wantPath)
				mustOK(t, "JSONL exists", err)
			}
			messages := []ai.Message{
				{ID: "user", Role: ai.RoleUser, Content: ai.Content{ai.TextBlock("question"), ai.ImageBlock(ai.Image{MediaType: "image/png", Data: "aGVsbG8="})}},
				{ID: "answer", Role: ai.RoleAssistant, Content: ai.Content{ai.ThinkingBlock("thought", "signature"), ai.ReasoningBlock(ai.ReasoningItem{ID: "r", EncryptedContent: "opaque"}), ai.ToolCallBlock(ai.ToolCall{ID: "call", Name: "Read", Input: "{}", Signature: []byte("signed")})}},
			}
			for i := range messages {
				mustOK(t, "message", a.Append(ctx, Record{SessionID: "s", Type: MessageAppended, Message: &messages[i]}))
			}
			mustOK(t, "state", a.Append(ctx, Record{SessionID: "s", Type: SessionStatePatched, State: &StatePatch{Title: new("original"), Mode: new("plan")}}))
			b := open()
			loaded, err := b.Load(ctx, "s")
			mustOK(t, "load", err)
			if !reflect.DeepEqual(loaded.Messages, messages) || loaded.State.Mode != "plan" {
				t.Fatalf("native replay changed: %+v", loaded)
			}
			checkList := func(title string, count int, sidechains bool) {
				t.Helper()
				list, err := a.List(ctx, "p", ListOptions{IncludeSidechain: sidechains})
				mustOK(t, "list", err)
				if len(list) != count {
					t.Fatalf("list = %+v, want %d entries", list, count)
				}
				for _, item := range list {
					if item.ID == "s" && item.Title != title {
						t.Fatalf("stale title: %+v", item)
					}
				}
			}
			checkList("original", 1, false)
			release, err := b.Claim("s")
			mustOK(t, "claim second owner", err)
			mustOK(t, "external title", b.Append(ctx, Record{SessionID: "s", Type: SessionStatePatched, State: &StatePatch{Title: new("renamed")}}))
			mustOK(t, "release", release())
			mustOK(t, "sidechain", b.Start(ctx, StartCommand{SessionID: "child", ParentSessionID: "s", Sidechain: true, Time: time.Now()}))
			checkList("renamed", 1, false)
			checkList("renamed", 2, true)
			checkList("renamed", 1, false)

			summary := ai.Message{ID: "summary", Role: ai.RoleUser, Content: ai.TextContent("summary")}
			last := ai.Message{ID: "last", Role: ai.RoleAssistant, Content: ai.TextContent("last")}
			for _, rec := range []Record{
				{Type: MessageAppended, Message: &summary},
				{Type: SessionCompacted, Compaction: &Compaction{MessageID: "summary"}},
				{Type: ConversationReplaced, Messages: []ai.Message{summary}},
				{Type: MessageAppended, Message: &summary},
				{Type: MessageAppended, Message: &last},
			} {
				rec.SessionID = "s"
				mustOK(t, "compact/reset", b.Append(ctx, rec))
			}
			mustOK(t, "close writer store", b.Close())
			reopened := open()
			resumed, err := reopened.Load(ctx, "s")
			mustOK(t, "resume", err)
			if !reflect.DeepEqual(resumed.Messages, []ai.Message{summary, last}) || resumed.State.Title != "renamed" {
				t.Fatalf("resume = %+v", resumed)
			}
			history, err := reopened.LoadHistory(ctx, "s")
			mustOK(t, "history", err)
			wantHistory := append(append([]ai.Message{}, messages...), summary, last)
			if !reflect.DeepEqual(history.Messages, wantHistory) {
				t.Fatalf("history = %+v", history.Messages)
			}
		})
	}
}

func TestBackendIdentityAndOwnership(t *testing.T) {
	for _, backend := range transcriptBackends {
		t.Run(backend.name, func(t *testing.T) {
			dir := t.TempDir()
			a, err := backend.open(dir, "p")
			mustOK(t, "open", err)
			t.Cleanup(func() { mustOK(t, "close a", a.Close()) })
			b, err := backend.open(dir, "p")
			mustOK(t, "open second", err)
			t.Cleanup(func() { mustOK(t, "close b", b.Close()) })
			ctx := context.Background()
			if exists, err := a.Exists(ctx, "missing"); err != nil || exists {
				t.Fatalf("missing = %v, %v", exists, err)
			}
			for _, id := range []string{"", "../escape", "a/b"} {
				if exists, err := a.Exists(ctx, id); err == nil || exists {
					t.Fatalf("invalid %q = %v, %v", id, exists, err)
				}
			}
			mustOK(t, "start", a.Start(ctx, StartCommand{SessionID: "s", Time: time.Now()}))
			if exists, err := b.Exists(ctx, "s"); err != nil || !exists {
				t.Fatalf("exists = %v, %v", exists, err)
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			for _, id := range []string{"s", "missing"} {
				if exists, err := a.Exists(cancelled, id); exists || !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled %q = %v, %v", id, exists, err)
				}
			}
			release, err := a.Claim("s")
			mustOK(t, "claim", err)
			t.Cleanup(func() { _ = release() })
			for _, s := range []*Store{a, b} {
				if other, err := s.Claim("s"); err == nil || (s == b && !errors.Is(err, sdksession.ErrBusy)) {
					if other != nil {
						_ = other()
					}
					t.Fatalf("second claim = %v", err)
				}
				if err := s.Delete(ctx, "s"); err == nil || (s == b && !errors.Is(err, sdksession.ErrBusy)) {
					t.Fatalf("delete claimed = %v", err)
				}
			}
			if err := a.Close(); !errors.Is(err, sdksession.ErrBusy) {
				t.Fatalf("close claimed = %v", err)
			}
			mustOK(t, "release", release())
			next, err := b.Claim("s")
			mustOK(t, "handoff", err)
			mustOK(t, "release second", next())
			mustOK(t, "delete", b.Delete(ctx, "s"))
			if exists, err := a.Exists(ctx, "s"); err != nil || exists {
				t.Fatalf("deleted = %v, %v", exists, err)
			}
			mustOK(t, "close after release", a.Close())
		})
	}
}
