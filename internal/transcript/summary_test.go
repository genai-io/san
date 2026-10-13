package transcript

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestWindowsMergeOnlyTheirOwnIndexEntries(t *testing.T) {
	dir := t.TempDir()
	a, err := NewStore(dir, "proj")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewStore(dir, "proj")
	if err != nil {
		t.Fatal(err)
	}
	startSession(t, a, "A")
	startSession(t, b, "B")
	if err := a.Append(context.Background(), Record{SessionID: "A", ParentMessageID: "A-m1", Time: time.Now(), Type: MessageAppended, Message: &ai.Message{ID: "A2", Role: ai.RoleUser}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Flush(); err != nil {
		t.Fatal(err)
	}
	for _, store := range []*Store{a, b} {
		if ids := listIDs(t, store); !contains(ids, "A") || !contains(ids, "B") {
			t.Fatalf("visible sessions = %v", ids)
		}
	}
	if err := b.Delete(context.Background(), "B"); err != nil {
		t.Fatal(err)
	}
	startSession(t, a, "C")
	if ids := listIDs(t, b); contains(ids, "B") || !contains(ids, "C") {
		t.Fatalf("stale index after deletion = %v", ids)
	}
}

func TestConcurrentWindowsKeepAllSessionIndexEntries(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for window := range 2 {
		store, err := NewStore(dir, "proj")
		if err != nil {
			t.Fatal(err)
		}
		wg.Go(func() {
			for i := range 10 {
				id := fmt.Sprintf("window-%d-%d", window, i)
				if err := store.Start(context.Background(), StartCommand{SessionID: id, Time: time.Now()}); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	store, err := NewStore(dir, "proj")
	if err != nil {
		t.Fatal(err)
	}
	if ids := listIDs(t, store); len(ids) != 20 {
		t.Fatalf("sessions = %d, want 20", len(ids))
	}
}

// startSession writes a transcript the way a real session does.
func startSession(t *testing.T, fs *Store, id string) {
	t.Helper()
	if err := fs.Start(context.Background(), StartCommand{
		SessionID: id,
		Provider:  "anthropic",
		Model:     "model",
		Time:      time.Now(),
	}); err != nil {
		t.Fatalf("Start(%s): %v", id, err)
	}
	if err := fs.Append(context.Background(), Record{SessionID: id, Time: time.Now(), Type: MessageAppended, Message: &ai.Message{ID: id + "-m1", Role: "user", Content: ai.Content{ai.TextBlock("hello from " + id)}}}); err != nil {
		t.Fatalf("AppendMessage(%s): %v", id, err)
	}
	if err := fs.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}

func listIDs(t *testing.T, fs *Store) []string {
	t.Helper()
	items, err := fs.List(context.Background(), "proj", ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

// Losing a summary used to make every prior session permanently
// invisible in /resume. Start runs at the first turn of the next session, and
// it replaced the unreadable summary with an empty one — so by the time the
// picker looked, the file parsed fine and its own rebuild never fired. The
// .jsonl transcripts sat on disk with no way back to them.
func TestStartRebuildsARemovedSummaryInsteadOfDiscardingIt(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewStore(dir, "proj")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	startSession(t, fs, "older-1")
	startSession(t, fs, "older-2")

	// Restore from a backup that skipped the summary, or a truncating crash.
	if err := os.Remove(fs.summaryPath("older-1")); err != nil {
		t.Fatalf("remove summary: %v", err)
	}

	// A fresh process: new store, no cached summary, then a new session starts.
	reopened, err := NewStore(dir, "proj")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	startSession(t, reopened, "new-3")

	ids := listIDs(t, reopened)
	for _, want := range []string{"older-1", "older-2", "new-3"} {
		if !contains(ids, want) {
			t.Errorf("session %q is missing from the picker; got %v", want, ids)
		}
	}
}

// An unparseable summary is the same situation as a missing one.
func TestStartRebuildsACorruptSummary(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewStore(dir, "proj")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	startSession(t, fs, "older-1")

	if err := os.WriteFile(fs.summaryPath("older-1"), []byte("{ truncated"), 0o644); err != nil {
		t.Fatalf("corrupt summary: %v", err)
	}

	reopened, err := NewStore(dir, "proj")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	startSession(t, reopened, "new-2")

	if ids := listIDs(t, reopened); !contains(ids, "older-1") {
		t.Errorf("the pre-existing session was dropped; got %v", ids)
	}
}

// A brand new store has no transcripts to rebuild from; the empty summary is the
// right answer and must not be treated as a failure.
func TestStartOnAnEmptyStoreStillWorks(t *testing.T) {
	fs, err := NewStore(t.TempDir(), "proj")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	startSession(t, fs, "first")

	if ids := listIDs(t, fs); !contains(ids, "first") {
		t.Errorf("the first session is missing; got %v", ids)
	}
}

func contains(haystack []string, needle string) bool {
	return slices.Contains(haystack, needle)
}
