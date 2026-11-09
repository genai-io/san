package session

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/genai-io/san/internal/transcript"
	"github.com/genai-io/sdk-go/pkg/ai"
	sdksession "github.com/genai-io/sdk-go/pkg/session"
)

func eventStorageFixture(t *testing.T) (*Store, *Session, Event) {
	t.Helper()
	s, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	create := Event{Seq: 1, Type: EventSessionCreate, SchemaVersion: 1, Time: 123, Payload: SessionCreatePayload{ProjectID: s.projectID, Workspace: s.workspace, InitialCWD: s.cwd, AgentID: "main"}}
	opened, err := s.createEventSession(context.Background(), "create-batch", "session", create, EventValidationContext{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	return s, opened, create
}
func testEvent(seq uint64, _ string, p EventPayload) Event {
	return Event{Seq: seq, Type: p.eventType(), SchemaVersion: 1, Time: 123, Payload: p}
}
func commitTestEvents(t *testing.T, s *Session, id string, events ...Event) *preparedEventBatch {
	t.Helper()
	p, err := s.prepareEventBatch(id, events, EventValidationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.commitEventBatch(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return p
}
func eventBytes(t *testing.T, s *Session) []byte {
	t.Helper()
	b, err := os.ReadFile(s.TranscriptPath())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEventPersistenceRetryAndNativeHistory(t *testing.T) {
	ctx := context.Background()
	store, s, _ := eventStorageFixture(t)
	messages := []ai.Message{eventText("u", ai.RoleUser, "question"), eventText("a", ai.RoleUser, "followup")}
	appendEvent := testEvent(2, "append", MessageAppendPayload{Messages: messages})
	prepared := commitTestEvents(t, s, "messages", appendEvent)
	commitTestEvents(t, s, "compact", testEvent(3, "compact", ContextCompactPayload{Summary: eventText("summary", ai.RoleUser, "summary"), Through: 2}))
	before := eventBytes(t, s)
	if _, err := s.commitEventBatch(ctx, prepared); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.eventSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Events()) != 3 || len(snapshot.Messages()) != 3 || len(snapshot.ActiveMessages()) != 1 {
		t.Fatal("retry lost compaction or full history")
	}
	if !reflect.DeepEqual(snapshot.Messages()[:2], messages) {
		t.Fatal("native message fidelity")
	}
	if err = s.Append(ctx, transcript.Record{Type: transcript.MessageAppended}); !errors.Is(err, transcript.ErrEventFormat) {
		t.Fatal(err)
	}
	if !bytes.Equal(before, eventBytes(t, s)) {
		t.Fatal("retry/legacy append changed bytes")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.openEventSession(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	retry, err := reopened.prepareEventBatch("messages", []Event{appendEvent}, EventValidationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.commitEventBatch(ctx, retry); err != nil {
		t.Fatal(err)
	}
	latest, _ := reopened.eventSnapshot()
	if len(latest.Events()) != 3 {
		t.Fatal("older retry published stale projection")
	}
	if !bytes.Equal(before, eventBytes(t, reopened)) {
		t.Fatal("reopen retry changed bytes")
	}
	read, tail, err := store.replayEvents(ctx, "session", nil)
	if err != nil {
		t.Fatal(err)
	}
	if tail.Count != 3 || tail.Sequence != 3 || !reflect.DeepEqual(read.Events(), snapshot.Events()) {
		t.Fatal("replay mismatch")
	}
}
func TestEventConflictsAndStalePrepare(t *testing.T) {
	ctx := context.Background()
	_, s, _ := eventStorageFixture(t)
	e := testEvent(2, "note", AnnotationAddPayload{Text: "one"})
	first, err := s.prepareEventBatch("first", []Event{e}, EventValidationContext{})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := s.prepareEventBatch("stale", []Event{testEvent(2, "other", AnnotationAddPayload{Text: "other"})}, EventValidationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.commitEventBatch(ctx, first); err != nil {
		t.Fatal(err)
	}
	before := eventBytes(t, s)
	if _, err = s.commitEventBatch(ctx, stale); !errors.Is(err, sdksession.ErrTail) {
		t.Fatal(err)
	}
	if _, err = s.prepareEventBatch("new-id", []Event{e}, EventValidationContext{}); err == nil {
		t.Fatal("event ID reuse accepted")
	}
	e.Payload = AnnotationAddPayload{Text: "different"}
	conflict, err := s.prepareEventBatch("first", []Event{e}, EventValidationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.commitEventBatch(ctx, conflict); !errors.Is(err, sdksession.ErrConflict) {
		t.Fatal(err)
	}
	if s.Err() != nil {
		t.Fatal("deterministic conflict poisoned writer")
	}
	if !bytes.Equal(before, eventBytes(t, s)) {
		t.Fatal("conflicts changed bytes")
	}
	commitTestEvents(t, s, "healthy", testEvent(3, "healthy", AnnotationAddPayload{Text: "still healthy"}))
}
func TestEventReadOnlyAndRecovery(t *testing.T) {
	ctx := context.Background()
	store, s, _ := eventStorageFixture(t)
	before := eventBytes(t, s)
	_ = s.Close()
	f, err := os.OpenFile(s.TranscriptPath(), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"batch_version":1`)
	_ = f.Close()
	damaged := eventBytes(t, s)
	if p, _, err := store.replayEvents(ctx, "session", nil); err != nil || len(p.Events()) != 1 {
		t.Fatalf("readonly committed prefix: %v", err)
	}
	if !bytes.Equal(damaged, eventBytes(t, s)) {
		t.Fatal("readonly repaired bytes")
	}
	reopened, err := store.openEventSession(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !bytes.Equal(before, eventBytes(t, s)) {
		t.Fatal("writer did not recover tail")
	}
	if err := store.Delete("session"); err == nil {
		t.Fatal("deleted claimed session")
	}
	_ = reopened.Close()
	if err := store.Delete("session"); err != nil {
		t.Fatal(err)
	}
}
func TestEventMalformedInitialUnchanged(t *testing.T) {
	ctx := context.Background()
	store, s, _ := eventStorageFixture(t)
	original := eventBytes(t, s)
	_ = s.Close()
	if err := os.WriteFile(s.TranscriptPath(), original[:len(original)/2], 0600); err != nil {
		t.Fatal(err)
	}
	before := eventBytes(t, s)
	if _, err := store.openEventSession(ctx, "session"); err == nil {
		t.Fatal("accepted malformed initial")
	}
	if !bytes.Equal(before, eventBytes(t, s)) {
		t.Fatal("malformed initial was repaired")
	}
}
func TestEventEvidenceByBatch(t *testing.T) {
	ctx := context.Background()
	store, s, _ := eventStorageFixture(t)
	child, err := store.Create(CreateOptions{ID: "child", AgentID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Close(); err != nil {
		t.Fatal(err)
	}
	key := foreignKey("child", 1)
	facts := EventValidationContext{ForeignEvents: map[string]ForeignEventEvidence{key: {Type: EventMessageAppend, MessageIDs: []string{"source"}}}}
	e := testEvent(2, "copy", MessageAppendPayload{Messages: []ai.Message{eventText("copy-message", ai.RoleAssistant, "copied")}})
	e.Source = &EventRef{Session: "child", Seq: 1}
	if _, err := s.prepareEventBatch("missing", []Event{e}, EventValidationContext{}); err == nil {
		t.Fatal("future evidence validated old event")
	}
	p, err := s.prepareEventBatch("copy", []Event{e}, facts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.commitEventBatch(ctx, p); err != nil {
		t.Fatal(err)
	}
	commitTestEvents(t, s, "next", testEvent(3, "note", AnnotationAddPayload{Text: "next"}))
	facts.ForeignEvents[key] = ForeignEventEvidence{Type: EventAnnotationAdd}
	if _, err := s.prepareEventBatch("contradiction", []Event{testEvent(4, "bad", AnnotationAddPayload{Text: "bad"})}, facts); err == nil {
		t.Fatal("contradictory evidence accepted")
	}
	if err = store.Delete("child"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	reopened, err := store.openEventSession(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	p2, _ := reopened.eventSnapshot()
	if len(p2.Messages()) != 1 {
		t.Fatal("lost retained evidence")
	}
}

func TestEventLostAcknowledgmentReopenRetry(t *testing.T) {
	ctx := context.Background()
	store, s, _ := eventStorageFixture(t)
	events := []Event{testEvent(2, "first", AnnotationAddPayload{Text: "first"}), testEvent(3, "second", AnnotationAddPayload{Text: "second"})}
	prepared, err := s.prepareEventBatch("lost-ack", events, EventValidationContext{})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := store.transcriptStore.CommitBatch(ctx, s.ID(), prepared.batch)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Tail.Count != 2 || receipt.Tail.Sequence != 2 {
		t.Fatal("SDK counts should count records/batches, not events")
	}
	old, err := s.eventSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(old.Events()) != 1 {
		t.Fatal("unacknowledged commit published candidate")
	}
	before := eventBytes(t, s)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.openEventSession(ctx, s.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replayed, _ := reopened.eventSnapshot()
	if len(replayed.Events()) != 3 {
		t.Fatal("durable unacknowledged batch missing")
	}
	retry, err := reopened.prepareEventBatch("lost-ack", events, EventValidationContext{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.commitEventBatch(ctx, retry)
	if err != nil {
		t.Fatal(err)
	}
	if got != receipt {
		t.Fatal("retry receipt differs")
	}
	latest, _ := reopened.eventSnapshot()
	if len(latest.Events()) != 3 {
		t.Fatal("retry duplicated or replaced events")
	}
	if !bytes.Equal(before, eventBytes(t, reopened)) {
		t.Fatal("lost-ack retry wrote bytes")
	}
}
