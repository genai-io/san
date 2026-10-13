package transcript

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestRecoverContinueAndRecoverAgain(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, "p")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Start(ctx, StartCommand{SessionID: "s", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(ctx, Record{SessionID: "s", Time: time.Now(), Type: MessageAppended, Message: &ai.Message{ID: "m1", Role: ai.RoleUser, Content: ai.TextContent("before")}}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(s.TranscriptPath("s"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"id":"incomplete"`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewStore(dir, "p")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"m2", "m3"} {
		if err := s.Append(ctx, Record{SessionID: "s", Time: time.Now(), Type: MessageAppended, Message: &ai.Message{ID: id, Role: ai.RoleUser, Content: ai.TextContent(id)}}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Load(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("recovered conversation lost messages: %+v", got.Messages)
	}
}

func TestIndependentSessionsCannotOverwriteEachOthersSummaries(t *testing.T) {
	dir := t.TempDir()
	a, err := NewStore(dir, "p")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewStore(dir, "p")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for id, s := range map[string]*Store{"a": a, "b": b} {
		if err := s.Start(ctx, StartCommand{SessionID: id, Time: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if err := s.Append(ctx, Record{SessionID: id, Time: time.Now(), Type: MessageAppended, Message: &ai.Message{ID: "m", Role: ai.RoleUser, Content: ai.TextContent(id)}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Flush(); err != nil {
		t.Fatal(err)
	}
	fresh, err := NewStore(dir, "p")
	if err != nil {
		t.Fatal(err)
	}
	list, err := fresh.List(ctx, "p", ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].MessageCount != 1 || list[1].MessageCount != 1 {
		t.Fatalf("fresh list disagrees with logs: %+v", list)
	}
}

func TestWriterLeaseRejectsASecondStore(t *testing.T) {
	dir := t.TempDir()
	a, err := NewStore(dir, "p")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewStore(dir, "p")
	if err != nil {
		t.Fatal(err)
	}
	release, err := a.Claim("s")
	if err != nil {
		t.Fatal(err)
	}
	if other, err := a.Claim("s"); err == nil {
		other()
		t.Fatal("same store granted two writer leases")
	}
	if other, err := b.Claim("s"); err == nil {
		other()
		t.Fatal("two stores owned the same writer lease")
	}
	release()
	other, err := b.Claim("s")
	if err != nil {
		t.Fatal(err)
	}
	other()
}

func TestOldFormatIsRejected(t *testing.T) {
	s, err := NewStore(t.TempDir(), "p")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.TranscriptPath("old"), []byte("{\"type\":\"session.started\",\"version\":\"1\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(context.Background(), "old"); err == nil {
		t.Fatal("old transcript format was accepted")
	}
}
