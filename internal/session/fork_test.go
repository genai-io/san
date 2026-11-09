package session

import (
	"bytes"
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

var forkNow = time.Date(2026, 11, 9, 15, 0, 0, 0, time.UTC)

// forkSource builds a session with a group hint, a foreign-sourced message and
// a standalone model call whose request copy lives in blobs/.
func forkSource(t *testing.T, store *Store) *eventSession {
	t.Helper()
	ctx := context.Background()
	create := Event{Seq: 1, Type: EventSessionCreate, SchemaVersion: 1, Time: 1000, Payload: SessionCreatePayload{
		ProjectID: store.projectID, Workspace: store.workspace, InitialCWD: store.cwd, AgentID: "main", Parent: &ParentLink{Session: "boss"}}}
	src, err := store.createSession(ctx, "c", "src", create, EventValidationContext{ParentAncestors: map[string][]string{"boss": {"boss"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = src.Close() })
	digest, err := blobStore{dir: filepath.Join(store.sessionDir("src"), "blobs")}.PutRequest(StandaloneRequest{System: "judge", Messages: []ai.Message{eventText("q", ai.RoleUser, "review this")}})
	if err != nil {
		t.Fatal(err)
	}
	provider, model := "p", "m"
	foreign := EventValidationContext{ForeignEvents: map[string]ForeignEventEvidence{foreignKey("child", 1): {Type: EventMessageAppend, MessageIDs: []string{"c1"}}}}
	for _, step := range []struct {
		id  string
		v   EventValidationContext
		evt Event
	}{
		{"m", EventValidationContext{}, testEvent(2, "", MessageAppendPayload{Messages: []ai.Message{eventText("u1", ai.RoleUser, "hello")}})},
		{"g", EventValidationContext{}, testEvent(3, "", SessionUpdatePayload{State: &StatePatch{GroupHint: SetField(GroupHint{GroupID: "g", MemberID: "me"})}})},
		{"f", foreign, func() Event {
			e := testEvent(4, "", MessageAppendPayload{Messages: []ai.Message{eventText("from-child", ai.RoleAssistant, "done")}})
			e.Source = &EventRef{Session: "child", Seq: 1}
			return e
		}()},
		{"i", EventValidationContext{}, testEvent(5, "", InferenceStartPayload{Purpose: PurposeJudge, Request: EventConfig{Provider: &provider, Model: &model}, Input: InferenceInput{Standalone: digest}})},
	} {
		p, err := src.prepareEventBatch(step.id, []Event{step.evt}, step.v)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := src.commitEventBatch(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

func TestForkCopiesThePrefixAndStaysIndependent(t *testing.T) {
	ctx := context.Background()
	store, err := openStoreIn(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	src := forkSource(t, store)
	before := eventBytes(t, src)
	fork, err := store.forkSession(ctx, "src", "fork", 0, forkNow)
	if err != nil {
		t.Fatal(err)
	}
	defer fork.Close()
	if !bytes.Equal(before, eventBytes(t, src)) {
		t.Fatal("fork changed its source")
	}
	got, _ := fork.eventSnapshot()
	want, _ := src.eventSnapshot()
	ge, we := got.Events(), want.Events()
	if len(ge) != len(we)+1 || ge[len(ge)-1].Type != EventSessionUpdate {
		t.Fatalf("want the source events plus one group-hint clear, got %d vs %d", len(ge), len(we))
	}
	if !reflect.DeepEqual(ge[1:len(we)], we[1:]) {
		t.Fatal("copied events differ from the source's")
	}
	id := got.Identity()
	if id.Fork == nil || id.Fork.Session != "src" || id.Fork.Seq != 5 || id.Parent != nil || id.AgentID != "main" {
		t.Fatalf("identity: %+v", id)
	}
	if got.State().GroupHint != nil || want.State().GroupHint == nil {
		t.Fatal("group hint must be cleared in the fork only")
	}
	if !reflect.DeepEqual(got.Messages(), want.Messages()) || !reflect.DeepEqual(got.Unfinished(), want.Unfinished()) {
		t.Fatal("history or unfinished work changed in the copy")
	}
	if u := got.Unfinished(); !reflect.DeepEqual(u.Inferences, []uint64{5}) {
		t.Fatalf("unfinished inference must stay unfinished: %+v", u)
	}
	// The request copy is the fork's own.
	if _, err := (blobStore{dir: filepath.Join(store.sessionDir("fork"), "blobs")}).Request(want.Events()[4].Payload.(*InferenceStartPayload).Input.Standalone); err != nil {
		t.Fatalf("blob not copied: %v", err)
	}
	// Deleting the source breaks nothing.
	_ = src.Close()
	if err := os.RemoveAll(store.sessionDir("src")); err != nil {
		t.Fatal(err)
	}
	_ = fork.Close()
	reopened, err := store.openSession(ctx, "fork")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := store.openSession(ctx, "fork"); !errors.Is(err, sdksession.ErrBusy) {
		t.Fatalf("a fork is claimed by its opener: %v", err)
	}
	if ov, _ := store.overview(ctx, "fork"); ov.LastActive != forkNow.UnixMilli() || ov.ForkedFrom != "src" {
		t.Fatalf("a new fork is activity now: %+v", ov)
	}
}

func TestForkAtAnEarlierPoint(t *testing.T) {
	ctx := context.Background()
	store, _ := openStoreIn(t.TempDir())
	defer store.Close()
	forkSource(t, store)
	fork, err := store.forkSession(ctx, "src", "early", 2, forkNow)
	if err != nil {
		t.Fatal(err)
	}
	defer fork.Close()
	p, _ := fork.eventSnapshot()
	if len(p.Events()) != 2 || p.Identity().Fork.Seq != 2 || len(p.Messages()) != 1 {
		t.Fatalf("a fork at 2 has %d events", len(p.Events()))
	}
	if _, err := os.Stat(filepath.Join(store.sessionDir("early"), "blobs")); !os.IsNotExist(err) {
		t.Fatal("blobs the prefix does not reference were copied")
	}
	only, err := store.forkSession(ctx, "src", "just-create", 1, forkNow)
	if err != nil {
		t.Fatal(err)
	}
	defer only.Close()
	if p, _ := only.eventSnapshot(); len(p.Events()) != 1 {
		t.Fatal("fork at 1 keeps only the creation")
	}
}

func TestForkFailuresLeaveNothing(t *testing.T) {
	ctx := context.Background()
	store, _ := openStoreIn(t.TempDir())
	defer store.Close()
	forkSource(t, store)
	for name, c := range map[string]struct {
		src, dst string
		at       uint64
	}{
		"past the end":   {"src", "x", 99},
		"unknown source": {"nope", "x", 0},
		"bad id":         {"src", "../x", 0},
		"existing":       {"src", "src", 0},
	} {
		if _, err := store.forkSession(ctx, c.src, c.dst, c.at, forkNow); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	// A missing request copy fails the fork rather than producing a dangling one.
	if err := os.RemoveAll(filepath.Join(store.sessionDir("src"), "blobs")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.forkSession(ctx, "src", "dangling", 0, forkNow); err == nil {
		t.Fatal("forked with a missing blob")
	}
	entries, _ := os.ReadDir(store.sessionsRoot())
	if len(entries) != 1 || entries[0].Name() != "src" {
		t.Fatalf("failed forks left directories: %v", entries)
	}
}
