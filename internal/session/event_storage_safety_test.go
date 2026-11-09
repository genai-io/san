package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai"
	sdksession "github.com/genai-io/sdk-go/pkg/session"
)

func TestEventIdentityAndOwnership(t *testing.T) {
	ctx := context.Background()
	store, s, create := eventStorageFixture(t)
	if _, err := store.createEventSession(ctx, "again", "session", create, EventValidationContext{}); err == nil {
		t.Fatal("overwrote existing session")
	}
	other, err := NewStoreWithDir(store.projectDir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err = other.openEventSession(ctx, "session"); !errors.Is(err, sdksession.ErrBusy) {
		t.Fatalf("ownership: %v", err)
	}
	wrong := &Store{projectID: "wrong", projectDir: store.projectDir}
	if _, _, err = wrong.replayEvents(ctx, "session", nil); err == nil {
		t.Fatal("accepted wrong project")
	}
	bad := create
	identity := bad.Payload.(SessionCreatePayload)
	identity.ProjectID = "wrong"
	bad.Payload = identity
	if _, err = store.createEventSession(ctx, "bad", "bad", bad, EventValidationContext{}); err == nil {
		t.Fatal("created wrong project")
	}
	if _, err = os.Stat(store.sessionDir("bad")); !os.IsNotExist(err) {
		t.Fatal("validation wrote a file")
	}
	identity.ProjectID = store.projectID
	identity.Workspace = "/another-project"
	bad.Payload = identity
	if _, err = store.createEventSession(ctx, "bad", "bad", bad, EventValidationContext{}); err == nil {
		t.Fatal("created wrong workspace")
	}
	_ = s.Close()
	_ = s.Close()
	reopened, err := other.openEventSession(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	_ = reopened.Close()
}
func TestEventConcurrentCreators(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	stores := make([]*Store, 2)
	for i := range stores {
		var err error
		stores[i], err = NewStoreWithDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer stores[i].Close()
	}
	create := testEvent(1, "create", SessionCreatePayload{ProjectID: stores[0].projectID, Workspace: dir, InitialCWD: dir, AgentID: "main"})
	start := make(chan struct{})
	results := make(chan *eventSession, 2)
	var wg sync.WaitGroup
	for _, store := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			s, _ := store.createEventSession(ctx, "create", "session", create, EventValidationContext{})
			results <- s
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	wins := 0
	for s := range results {
		if s != nil {
			wins++
			defer s.Close()
		}
	}
	if wins != 1 {
		t.Fatalf("creators succeeded: %d", wins)
	}
}
func TestEventStrictPayloadAndCorruption(t *testing.T) {
	for name, raw := range map[string]string{
		"version":   `{"version":2,"events":[],"validation":{}}`,
		"unknown":   `{"version":1,"events":[],"validation":{},"extra":true}`,
		"duplicate": `{"version":1,"version":1,"events":[],"validation":{}}`,
		"null":      `{"version":1,"events":[],"validation":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeEventBatchPayload([]byte(raw)); err == nil {
				t.Fatal("accepted malformed payload")
			}
		})
	}
	ctx := context.Background()
	store, s, _ := eventStorageFixture(t)
	_ = s.Close()
	original := eventBytes(t, s)
	damaged := bytes.Replace(original, []byte(`"agent_id":"main"`), []byte(`"agent_id":"evil"`), 1)
	if bytes.Equal(original, damaged) {
		t.Fatal("fixture not changed")
	}
	if err := os.WriteFile(s.Path(), damaged, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.replayEvents(ctx, "session", nil); err == nil {
		t.Fatal("accepted damaged hash")
	}
	if _, err := store.openEventSession(ctx, "session"); err == nil {
		t.Fatal("opened damaged hash")
	}
	if !bytes.Equal(damaged, eventBytes(t, s)) {
		t.Fatal("corruption modified")
	}
}
func TestEventReplayCannotUseFutureEvidence(t *testing.T) {
	ctx := context.Background()
	store, s, _ := eventStorageFixture(t)
	e := testEvent(2, "copy", MessageAppendPayload{Messages: []ai.Message{eventText("copied", ai.RoleAssistant, "copied")}})
	e.Source = &EventRef{Session: "foreign-session", Seq: 1}
	// Bypass product validation only to build a valid SDK envelope with invalid
	// product facts; the SDK remains the sole framing/hash authority.
	raw, err := json.Marshal(eventBatchPayload{1, []Event{e}, EventValidationContext{}})
	if err != nil {
		t.Fatal(err)
	}
	tail, err := s.log.tail()
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.log.commit(ctx, sdksession.Batch{ID: "invalid", Expected: tail, Records: []sdksession.Record{{Type: sdksession.TypeApp, Name: "san.events", Time: time.UnixMilli(e.Time).UTC(), Data: raw}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(eventBatchPayload{1, []Event{testEvent(3, "later", AnnotationAddPayload{Text: "later"})}, EventValidationContext{ForeignEvents: map[string]ForeignEventEvidence{foreignKey("foreign-session", 1): {Type: EventMessageAppend, MessageIDs: []string{"original"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.log.commit(ctx, sdksession.Batch{ID: "later", Expected: receipt.Tail, Records: []sdksession.Record{{Type: sdksession.TypeApp, Name: "san.events", Time: time.UnixMilli(e.Time).UTC(), Data: raw}}}); err != nil {
		t.Fatal(err)
	}
	before := eventBytes(t, s)
	_ = s.Close()
	if _, _, err = store.replayEvents(ctx, s.ID(), nil); err == nil {
		t.Fatal("future evidence repaired earlier fact")
	}
	if _, err = store.openEventSession(ctx, s.ID()); err == nil {
		t.Fatal("invalid prefix opened")
	}
	if !bytes.Equal(before, eventBytes(t, s)) {
		t.Fatal("invalid product data modified")
	}
	// Failure must release the lease even when the SDK log itself is valid.
	log, err := openEventLog(ctx, store.sessionDir(s.ID()))
	if err != nil {
		t.Fatal(err)
	}
	_ = log.close()
}
func TestEventPrepareDetachesInputAndCancellationPoisons(t *testing.T) {
	ctx := context.Background()
	_, s, _ := eventStorageFixture(t)
	text := AnnotationAddPayload{Text: "before"}
	e := testEvent(2, "note", &text)
	p, err := s.prepareEventBatch("note", []Event{e}, EventValidationContext{})
	if err != nil {
		t.Fatal(err)
	}
	text.Text = "after"
	if _, err = s.commitEventBatch(ctx, p); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := s.eventSnapshot()
	if snapshot.Events()[1].Payload.(*AnnotationAddPayload).Text != "before" {
		t.Fatal("input alias")
	}
	p, err = s.prepareEventBatch("cancel", []Event{testEvent(3, "cancel", AnnotationAddPayload{Text: "cancel"})}, EventValidationContext{})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.commitEventBatch(canceled, p); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !errors.Is(s.Err(), context.Canceled) {
		t.Fatal("cancellation not conservatively poisoned")
	}
}
