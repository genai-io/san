package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai"
	sdksession "github.com/genai-io/sdk-go/pkg/session"
)

func createTyped(t *testing.T, store *Store, opts CreateOptions) *Session {
	t.Helper()
	s, err := store.Create(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestTypedSessionAppendAndReopen(t *testing.T) {
	ctx := context.Background()
	store := cleanStore(t)
	s := createTyped(t, store, CreateOptions{ID: "typed"})
	initial := s.Snapshot()
	if initial.SessionID() != s.ID() || initial.Identity().InitialCWD != store.cwd || initial.Identity().AgentID != "main" {
		t.Fatalf("creation identity: %+v", initial.Identity())
	}
	if s.Path() != filepath.Join(store.projectDir, "sessions", "typed", "events.jsonl") {
		t.Fatal(s.Path())
	}
	payload := &MessageAppendPayload{Messages: []ai.Message{eventText("u", ai.RoleUser, "question")}}
	input := Event{Payload: payload}
	if err := s.Append(ctx, input, Event{Payload: AnnotationAddPayload{Text: "note"}}); err != nil {
		t.Fatal(err)
	}
	payload.Messages[0].ID = "changed"
	if input.Seq != 0 || input.SchemaVersion != 0 || input.Time != 0 || input.Type != "" {
		t.Fatal("Append modified the caller's envelope")
	}
	committed := s.Snapshot()
	if len(initial.Events()) != 1 || len(committed.Events()) != 3 || committed.Messages()[0].ID != "u" {
		t.Fatal("snapshots or payloads are not detached")
	}
	messages := committed.Messages()
	messages[0].ID = "also-changed"
	if committed.Messages()[0].ID != "u" {
		t.Fatal("projection accessor leaked storage")
	}
	batches := 0
	for batch, err := range readEventBatches(ctx, store.sessionDir(s.ID())) {
		if err != nil {
			t.Fatal(err)
		}
		batches++
		if batches == 2 {
			decoded, err := decodeEventBatchPayload(batch.Batch.Records[0].Data)
			if err != nil || len(decoded.Events) != 2 {
				t.Fatalf("Append split the batch: %+v %v", decoded, err)
			}
		}
	}
	if batches != 2 {
		t.Fatalf("got %d batches", batches)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(ctx, input); !errors.Is(err, ErrClosed) {
		t.Fatalf("append after close: %v", err)
	}
	closed := s.Snapshot()
	if !reflect.DeepEqual(closed.Events(), committed.Events()) {
		t.Fatal("Close lost the committed snapshot")
	}
	reopened, err := store.Open(ctx, s.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replayed := reopened.Snapshot()
	if !reflect.DeepEqual(replayed.Events(), committed.Events()) {
		t.Fatal("Open did not replay the committed prefix")
	}
	if err := reopened.Append(ctx, Event{Payload: AnnotationAddPayload{Text: "after reopen"}}); err != nil {
		t.Fatal(err)
	}
	last := reopened.Snapshot()
	if last.Events()[3].Seq != 4 {
		t.Fatal("sequence restarted on Open")
	}
}

func TestTypedSessionRejectsWholeInvalidBatch(t *testing.T) {
	store := cleanStore(t)
	s := createTyped(t, store, CreateOptions{})
	before, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	var nilPayload *AnnotationAddPayload
	bad := [][]Event{
		nil,
		{{Payload: AnnotationAddPayload{Text: "valid"}}, {Payload: AnnotationAddPayload{}}},
		{{Payload: nilPayload}},
		{{Payload: SessionCreatePayload{}}},
		{{Seq: 100, Payload: AnnotationAddPayload{Text: "stale"}}},
		{{SchemaVersion: 2, Payload: AnnotationAddPayload{Text: "future"}}},
		{{Type: EventMessageAppend, Payload: AnnotationAddPayload{Text: "mismatch"}}},
	}
	for i, events := range bad {
		if err := s.Append(context.Background(), events...); err == nil {
			t.Fatalf("accepted invalid batch %d", i)
		}
	}
	after, err := os.ReadFile(s.Path())
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("rejected batch changed bytes: %v", err)
	}
	if err := s.Err(); err != nil {
		t.Fatalf("validation error poisoned writer: %v", err)
	}
	if err := s.Append(context.Background(), Event{Payload: AnnotationAddPayload{Text: "still writable"}}); err != nil {
		t.Fatal(err)
	}
}

func TestTypedSessionSerializesConcurrentAppends(t *testing.T) {
	s := createTyped(t, cleanStore(t), CreateOptions{})
	const workers = 24
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := range workers {
		wg.Go(func() {
			errs <- s.Append(context.Background(), Event{Payload: AnnotationAddPayload{Text: fmt.Sprint(i)}})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot := s.Snapshot()
	if len(snapshot.Events()) != workers+1 {
		t.Fatal("lost concurrent appends")
	}
	for i, e := range snapshot.Events() {
		if e.Seq != uint64(i+1) {
			t.Fatal("noncontiguous sequence")
		}
	}
}

func TestTypedStoreReadAttrsAndTrash(t *testing.T) {
	ctx := context.Background()
	store := cleanStore(t)
	s := createTyped(t, store, CreateOptions{ID: "organized"})
	if _, err := store.Open(ctx, s.ID()); !errors.Is(err, sdksession.ErrBusy) {
		t.Fatalf("second writer: %v", err)
	}
	if _, err := store.Get(ctx, s.ID()); err != nil {
		t.Fatalf("reader needs writer: %v", err)
	}
	if err := store.SetAttrs(ctx, s.ID(), AttrsPatch{Title: SetField("Chosen"), Pinned: SetField(true)}); err != nil {
		t.Fatal(err)
	}
	page, err := store.List(ctx, ListQuery{})
	if err != nil || len(page.Items) != 1 || page.Items[0].DisplayTitle() != "Chosen" {
		t.Fatalf("list attrs: %+v %v", page, err)
	}
	if err := store.Delete(ctx, s.ID()); !errors.Is(err, sdksession.ErrBusy) {
		t.Fatalf("deleted an open session: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, s.ID()); !errors.Is(err, ErrPinned) {
		t.Fatalf("deleted a pinned session: %v", err)
	}
	if err := store.SetAttrs(ctx, s.ID(), AttrsPatch{Pinned: ClearField[bool]()}); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, s.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(ctx, s.ID()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("opened trash: %v", err)
	}
	if _, err := store.Fork(ctx, s.ID(), ForkOptions{}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("forked trash: %v", err)
	}
	page, err = store.List(ctx, ListQuery{Visibility: VisibleTrash})
	if err != nil || len(page.Items) != 1 || page.Items[0].Trashed == nil {
		t.Fatalf("trash list: %+v %v", page, err)
	}
	if err := store.Restore(ctx, s.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, s.ID()); err != nil {
		t.Fatal(err)
	}
}

func TestTypedStoreForkAndParentEvidence(t *testing.T) {
	ctx := context.Background()
	store := cleanStore(t)
	parent := createTyped(t, store, CreateOptions{ID: "parent"})
	if err := parent.Append(ctx, Event{Payload: MessageAppendPayload{Messages: []ai.Message{eventText("u", ai.RoleUser, "original")}}}); err != nil {
		t.Fatal(err)
	}
	child := createTyped(t, store, CreateOptions{ID: "child", Parent: &ParentLink{Session: parent.ID()}})
	grandchild := createTyped(t, store, CreateOptions{ID: "grandchild", Parent: &ParentLink{Session: child.ID()}})
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, child.ID()); err != nil {
		t.Fatal(err)
	}
	if err := grandchild.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(ctx, grandchild.ID())
	if err != nil {
		t.Fatalf("child ancestry needed surviving parent: %v", err)
	}
	defer reopened.Close()
	fork, err := store.Fork(ctx, parent.ID(), ForkOptions{Seq: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer fork.Close()
	snapshot := fork.Snapshot()
	if snapshot.Identity().Parent != nil || snapshot.Identity().Fork.Session != parent.ID() || snapshot.Messages()[0].ID != "u" {
		t.Fatal("fork identity or native IDs changed")
	}
	if _, err := store.Create(ctx, CreateOptions{ID: "missing-parent", Parent: &ParentLink{Session: "missing"}}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unvalidated parent: %v", err)
	}
}

func TestTypedSessionForeignSourceSurvivesPurge(t *testing.T) {
	ctx := context.Background()
	store := cleanStore(t)
	source := createTyped(t, store, CreateOptions{ID: "source"})
	if err := source.Append(ctx, Event{Payload: MessageAppendPayload{Messages: []ai.Message{eventText("original", ai.RoleUser, "delivered")}}}); err != nil {
		t.Fatal(err)
	}
	target := createTyped(t, store, CreateOptions{ID: "target"})
	delivery := Event{Source: &EventRef{Session: source.ID(), Seq: 2},
		Payload: MessageAppendPayload{Messages: []ai.Message{eventText("copy", ai.RoleAssistant, "delivered")}}}
	if err := target.Append(ctx, delivery); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	// Remove only this test's source fixture: replay must use retained evidence.
	if err := os.RemoveAll(store.sessionDir(source.ID())); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(ctx, target.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	snapshot := reopened.Snapshot()
	if snapshot.Messages()[0].ID != "copy" {
		t.Fatal("lost accepted delivery")
	}
	if err := reopened.Append(ctx, Event{Source: &EventRef{Session: "missing", Seq: 2}, Payload: AnnotationAddPayload{Text: "unverified"}}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("accepted nonexistent source: %v", err)
	}
}

func TestTypedSessionRequiresDurableRequestAndReportsUnfinished(t *testing.T) {
	ctx := context.Background()
	store := cleanStore(t)
	s := createTyped(t, store, CreateOptions{})
	request := StandaloneRequest{Messages: []ai.Message{eventText("input", ai.RoleUser, "judge")}}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	provider, model := "provider", "model"
	start := Event{Payload: InferenceStartPayload{Purpose: PurposeJudge, Request: EventConfig{Provider: &provider, Model: &model}, Input: InferenceInput{Standalone: blobDigest(raw)}}}
	if err := s.Append(ctx, start); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed missing request: %v", err)
	}
	if _, err := s.PutRequest(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(ctx, start); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(ctx, s.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	snapshot := reopened.Snapshot()
	if !reflect.DeepEqual(snapshot.Unfinished().Inferences, []uint64{2}) || len(snapshot.Events()) != 2 {
		t.Fatal("resume invented a finish or lost interrupted inference")
	}
}

func TestTypedSessionForeignCompactionIsRejected(t *testing.T) {
	ctx := context.Background()
	store := cleanStore(t)
	source := createTyped(t, store, CreateOptions{ID: "source"})
	if err := source.Append(ctx, Event{Payload: AnnotationAddPayload{Text: "two"}}, Event{Payload: AnnotationAddPayload{Text: "three"}}); err != nil {
		t.Fatal(err)
	}
	target := createTyped(t, store, CreateOptions{ID: "target"})
	if err := target.Append(ctx, Event{Payload: MessageAppendPayload{Messages: []ai.Message{eventText("u", ai.RoleUser, "question")}}}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(target.Path())
	if err != nil {
		t.Fatal(err)
	}
	bad := Event{Source: &EventRef{Session: source.ID(), Seq: 3},
		Payload: ContextCompactPayload{Through: 2, Summary: eventText("summary", ai.RoleUser, "summary")}}
	if err := target.Append(ctx, bad); err == nil {
		t.Fatal("accepted foreign compaction source")
	}
	after, err := os.ReadFile(target.Path())
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("invalid compaction changed bytes: %v", err)
	}
	if err := target.Append(ctx, Event{Payload: AnnotationAddPayload{Text: "still writable"}}); err != nil {
		t.Fatal(err)
	}
}

func TestTypedAttrsAndTrashShareOneMutationLock(t *testing.T) {
	ctx := context.Background()
	store := cleanStore(t)
	s := createTyped(t, store, CreateOptions{ID: "organized"})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	atGuard, release := make(chan struct{}), make(chan struct{})
	deleted := make(chan error, 1)
	go func() {
		deleted <- (library{root: store.projectDir}).trashChecked(ctx, s.ID(), t0, func(string) error {
			close(atGuard)
			<-release
			return nil
		})
	}()
	select {
	case <-atGuard:
	case err := <-deleted:
		t.Fatalf("trash failed before the guard: %v", err)
	}
	pinned := make(chan error, 1)
	go func() { pinned <- store.SetAttrs(ctx, s.ID(), AttrsPatch{Pinned: SetField(true)}) }()
	select {
	case err := <-pinned:
		close(release)
		t.Fatalf("attribute update bypassed a pending move: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	if err := <-pinned; !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pin committed into a moved directory: %v", err)
	}
	if _, err := os.Stat(store.sessionDir(s.ID())); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("attribute update created a ghost directory: %v", err)
	}
}

func TestTypedAppendAndBlobCollectionShareOneMutationLock(t *testing.T) {
	ctx := context.Background()
	store := cleanStore(t)
	s := createTyped(t, store, CreateOptions{})
	digest, err := s.PutRequest(ctx, StandaloneRequest{Messages: []ai.Message{eventText("u", ai.RoleUser, "judge")}})
	if err != nil {
		t.Fatal(err)
	}
	provider, model := "provider", "model"
	start := Event{Payload: &InferenceStartPayload{Purpose: PurposeJudge,
		Request: EventConfig{Provider: &provider, Model: &model}, Input: InferenceInput{Standalone: digest}}}
	collected := make(chan error, 1)
	removed := make(chan string, 1)
	err = (library{root: store.projectDir}).withLock(ctx, s.ID(), func() error {
		if _, err := s.validation(ctx, []Event{start}); err != nil {
			return err
		}
		go func() {
			collected <- store.collectBlobs(ctx, store.sessionDir(s.ID()), s.ID(), func(string) bool { return true }, func(path string) error {
				removed <- path
				return os.Remove(path)
			})
		}()
		select {
		case path := <-removed:
			return fmt.Errorf("collector raced request validation: %s", path)
		case <-time.After(20 * time.Millisecond):
		}
		return s.appendLocked(ctx, []Event{start})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-collected; err != nil {
		t.Fatal(err)
	}
	if _, err := s.Request(ctx, digest); err != nil {
		t.Fatalf("committed request was collected: %v", err)
	}
}

func TestTypedStoreCancellationCloseAndInvalidIDs(t *testing.T) {
	store := cleanStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operations := map[string]func(context.Context) error{
		"create": func(ctx context.Context) error {
			_, err := store.Create(ctx, CreateOptions{ID: "cancelled"})
			return err
		},
		"open":    func(ctx context.Context) error { _, err := store.Open(ctx, "missing"); return err },
		"fork":    func(ctx context.Context) error { _, err := store.Fork(ctx, "missing", ForkOptions{}); return err },
		"get":     func(ctx context.Context) error { _, err := store.Get(ctx, "missing"); return err },
		"list":    func(ctx context.Context) error { _, err := store.List(ctx, ListQuery{}); return err },
		"events":  func(ctx context.Context) error { _, err := store.Events(ctx, "missing", EventQuery{}); return err },
		"attrs":   func(ctx context.Context) error { return store.SetAttrs(ctx, "missing", AttrsPatch{}) },
		"delete":  func(ctx context.Context) error { return store.Delete(ctx, "missing") },
		"restore": func(ctx context.Context) error { return store.Restore(ctx, "missing") },
		"clean":   func(ctx context.Context) error { _, err := store.Clean(ctx, CleanOptions{}); return err },
	}
	for name, operation := range operations {
		if err := operation(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s cancellation: %v", name, err)
		}
	}
	for _, id := range []string{"", "..", "../outside", "a/b"} {
		if _, err := store.Open(context.Background(), id); err == nil {
			t.Fatalf("accepted ID %q", id)
		}
		if _, err := store.Get(context.Background(), id); err == nil {
			t.Fatalf("accepted ID %q", id)
		}
	}
	if err := store.SetAttrs(context.Background(), "missing", AttrsPatch{Pinned: SetField(true)}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("attrs created a ghost session: %v", err)
	}
	if _, err := os.Stat(store.sessionDir("missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("attrs created a directory: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for name, operation := range operations {
		if err := operation(context.Background()); !errors.Is(err, ErrClosed) {
			t.Fatalf("%s after Close: %v", name, err)
		}
	}
}
