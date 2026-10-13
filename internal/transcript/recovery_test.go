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

func TestWriterLeaseRefreshesExternalHistoryAndCounters(t *testing.T) {
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
	if err := a.Start(ctx, StartCommand{SessionID: "s", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	appendMessage := func(s *Store, id string) {
		t.Helper()
		if err := s.Append(ctx, Record{SessionID: "s", Type: MessageAppended, Message: &ai.Message{ID: id, Role: ai.RoleUser, Content: ai.TextContent(id)}}); err != nil {
			t.Fatal(err)
		}
	}
	appendMessage(a, "m1")
	if _, err := b.Load(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	release, err := a.Claim("s")
	if err != nil {
		t.Fatal(err)
	}
	appendMessage(a, "m2")
	if err := a.Append(ctx, Record{SessionID: "s", Type: InferenceRequested, Inference: &InferenceRecord{Sequence: 2, Exchange: 3}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Append(ctx, Record{SessionID: "s", Type: SessionStatePatched, State: &StateRecord{Ops: []PatchOp{PatchTitle("updated by A")}}}); err != nil {
		t.Fatal(err)
	}
	release()
	release, err = b.Claim("s")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	leaf, inference, exchange, err := b.Position(ctx, "s")
	if err != nil || leaf != "m2" || inference != 2 || exchange != 3 {
		t.Fatalf("position after lease handoff = %q, %d, %d, %v", leaf, inference, exchange, err)
	}
	state, err := b.LoadState(ctx, "s")
	if err != nil || state.Title != "updated by A" {
		t.Fatalf("state after lease handoff = %+v, %v", state, err)
	}
	appendMessage(b, "m3")
	got, err := a.Load(ctx, "s")
	if err != nil || len(got.Messages) != 3 || got.Messages[1].ID != "m2" {
		t.Fatalf("writer handoff lost intervening messages: %+v, %v", got, err)
	}
}

func TestWriterLeaseRepairsANewTornTail(t *testing.T) {
	s, err := NewStore(t.TempDir(), "p")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Start(ctx, StartCommand{SessionID: "s", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	write := func(id string) {
		t.Helper()
		release, err := s.Claim("s")
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		if err := s.Append(ctx, Record{SessionID: "s", Type: MessageAppended, Message: &ai.Message{ID: id, Role: ai.RoleUser, Content: ai.TextContent(id)}}); err != nil {
			t.Fatal(err)
		}
	}
	write("m1")
	// Simulate another process crashing during its next write.
	f, err := os.OpenFile(s.TranscriptPath("s"), os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"id":"torn"`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	write("m2")
	got, err := s.Load(ctx, "s")
	if err != nil || len(got.Messages) != 2 {
		t.Fatalf("reclaimed writer did not repair the tail: %+v, %v", got, err)
	}
}
