package session

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func queryFixture(t *testing.T, store *Store, id string) *eventSession {
	t.Helper()
	create := Event{Seq: 1, Type: EventSessionCreate, SchemaVersion: 1, Time: 1700000000000,
		Payload: &SessionCreatePayload{ProjectID: store.projectID, Workspace: store.workspace, InitialCWD: store.cwd, AgentID: "main"}}
	session, err := store.createSession(context.Background(), id+"-initial", id, create, EventValidationContext{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	queryAppend(t, session, 2, 5)
	return session
}

func queryAppend(t *testing.T, session *eventSession, first, last uint64) {
	t.Helper()
	var events []Event
	for seq := first; seq <= last; seq++ {
		message := ai.UserMessage(fmt.Sprintf("message %d", seq))
		message.ID = fmt.Sprintf("m-%d", seq)
		events = append(events, Event{Seq: seq,
			Type: EventMessageAppend, SchemaVersion: 1, Time: 1700000000000 + int64(seq), Payload: &MessageAppendPayload{Messages: []ai.Message{message}}})
	}
	prepared, err := session.prepareEventBatch(fmt.Sprintf("batch-%d", first), events, EventValidationContext{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.commitEventBatch(context.Background(), prepared); err != nil {
		t.Fatal(err)
	}
}

func TestEventPagesKeepCheckedPrefix(t *testing.T) {
	store, err := openStoreIn(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session := queryFixture(t, store, "typed")
	ctx := context.Background()
	first, err := store.Events(ctx, session.ID(), EventQuery{Limit: 2})
	if err != nil || len(first.Events) != 2 || first.NextCursor == "" || first.ThroughSeq != 5 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	// Returned payloads are caller-owned; edits must not affect later reads.
	first.Events[1].Payload.(*MessageAppendPayload).Messages[0].Content[0].Text = "changed"
	queryAppend(t, session, 6, 6)
	second, err := store.Events(ctx, session.ID(), EventQuery{Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(second.Events) != 2 || second.Events[0].Seq != 3 || second.ThroughSeq != 5 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	third, err := store.Events(ctx, session.ID(), EventQuery{Limit: 10, Cursor: second.NextCursor})
	if err != nil || len(third.Events) != 1 || third.Events[0].Seq != 5 || third.NextCursor != "" || third.ThroughSeq != 5 {
		t.Fatalf("third=%+v err=%v", third, err)
	}
	latest, err := store.Events(ctx, session.ID(), EventQuery{})
	if err != nil || len(latest.Events) != 6 || latest.ThroughSeq != 6 || latest.Events[1].Payload.(*MessageAppendPayload).Messages[0].Text() != "message 2" {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
}

func TestEventCursorRejectsChangedQueryOrPrefix(t *testing.T) {
	store, err := openStoreIn(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	queryFixture(t, store, "typed")
	first, err := store.Events(context.Background(), "typed", EventQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	data, err := base64.RawURLEncoding.DecodeString(first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	var valid eventCursor
	if err := json.Unmarshal(data, &valid); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*eventCursor){
		"version": func(c *eventCursor) { c.Version++ },
		"project": func(c *eventCursor) { c.ProjectID = "other" },
		"session": func(c *eventCursor) { c.SessionID = "other" },
		"offset":  func(c *eventCursor) { c.After = c.Through + 1 },
		"count":   func(c *eventCursor) { c.Through++ },
		"hash": func(c *eventCursor) {
			if strings.HasPrefix(c.Tail.Hash, "0") {
				c.Tail.Hash = "1" + c.Tail.Hash[1:]
			} else {
				c.Tail.Hash = "0" + c.Tail.Hash[1:]
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			cursor := valid
			change(&cursor)
			encoded, err := json.Marshal(cursor)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Events(context.Background(), "typed", EventQuery{Cursor: base64.RawURLEncoding.EncodeToString(encoded)}); err == nil {
				t.Fatal("accepted changed cursor")
			}
		})
	}
	for _, query := range []EventQuery{{Limit: -1}, {Limit: maxEventPageSize + 1}, {Cursor: "!"}, {Cursor: strings.Repeat("x", maxEventCursorSize+1)}} {
		if _, err := store.Events(context.Background(), "typed", query); !errors.Is(err, ErrInvalidEventQuery) {
			t.Fatalf("query=%+v err=%v", query, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Events(ctx, "typed", EventQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query: %v", err)
	}
}

func TestEventPinnedPageStopsBeforeLaterCorruption(t *testing.T) {
	store, err := openStoreIn(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session := queryFixture(t, store, "typed")
	page, err := store.Events(context.Background(), "typed", EventQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	path := store.eventsPath("typed")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := append(bytes.Clone(before), []byte("{\"batch_version\":99}\n")...)
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	rest, err := store.Events(context.Background(), "typed", EventQuery{Cursor: page.NextCursor})
	if err != nil || len(rest.Events) != 4 || rest.ThroughSeq != 5 {
		t.Fatalf("pinned page=%+v err=%v", rest, err)
	}
	if _, err := store.Events(context.Background(), "typed", EventQuery{}); err == nil {
		t.Fatal("fresh query ignored complete corruption")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(corrupt, after) {
		t.Fatalf("read changed log: %v", err)
	}
}

func TestEventsDoesNotReadLegacyTranscripts(t *testing.T) {
	dir := t.TempDir()
	legacy, err := NewLegacyStoreWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	session, err := legacy.Create(LegacyCreateOptions{ID: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(legacy.TranscriptPath("legacy"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := openStoreIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Events(context.Background(), "legacy", EventQuery{}); err == nil {
		t.Fatal("legacy records were presented as new events")
	}
	after, err := os.ReadFile(legacy.TranscriptPath("legacy"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("read changed legacy file: %v", err)
	}
}
