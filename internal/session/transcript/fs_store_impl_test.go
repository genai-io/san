package transcript

import (
	"context"
	"testing"
	"time"
)

func TestFileStoreStartAppendListLoad(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, "proj-1")
	if err != nil {
		t.Fatalf("NewFileStore(): %v", err)
	}

	now := time.Date(2026, 4, 6, 16, 0, 0, 0, time.UTC)
	if err := store.Start(context.Background(), StartCommand{
		SessionID: "tx-1",
		ProjectID: "proj-1",
		Cwd:       "/tmp/project",
		Provider:  "openai",
		Model:     "gpt-test",
		Time:      now,
	}); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if err := store.AppendMessage(context.Background(), AppendMessageCommand{
		SessionID: "tx-1",
		MessageID: "m1",
		Time:      now.Add(time.Second),
		Role:      "user",
		Content:   []ContentBlock{{Type: "text", Text: "hello"}},
	}); err != nil {
		t.Fatalf("AppendMessage(user): %v", err)
	}
	if err := store.AppendMessage(context.Background(), AppendMessageCommand{
		SessionID: "tx-1",
		MessageID: "m2",
		ParentID:  "m1",
		Time:      now.Add(2 * time.Second),
		Role:      "assistant",
		Content:   []ContentBlock{{Type: "text", Text: "world"}},
		GitBranch: "main",
	}); err != nil {
		t.Fatalf("AppendMessage(assistant): %v", err)
	}
	if err := store.PatchState(context.Background(), PatchStateCommand{
		SessionID: "tx-1",
		Time:      now.Add(3 * time.Second),
		Ops:       []PatchOp{PatchTitle("Fix bug"), PatchLastPrompt("hello")},
	}); err != nil {
		t.Fatalf("PatchState(): %v", err)
	}

	items, err := store.List(context.Background(), "proj-1", ListOptions{})
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 list item, got %d", len(items))
	}
	if items[0].Title != "Fix bug" {
		t.Fatalf("Title = %q, want %q", items[0].Title, "Fix bug")
	}
	if items[0].LastPrompt != "hello" {
		t.Fatalf("LastPrompt = %q, want %q", items[0].LastPrompt, "hello")
	}

	transcript, err := store.Load(context.Background(), "tx-1")
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if transcript.State.Title != "Fix bug" {
		t.Fatalf("projected title = %q, want %q", transcript.State.Title, "Fix bug")
	}
	if len(transcript.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(transcript.Messages))
	}
}

func TestFileStoreCompactAndFork(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, "proj-1")
	if err != nil {
		t.Fatalf("NewFileStore(): %v", err)
	}

	now := time.Date(2026, 4, 6, 16, 10, 0, 0, time.UTC)
	if err := store.Start(context.Background(), StartCommand{
		SessionID: "tx-1",
		ProjectID: "proj-1",
		Cwd:       "/tmp/project",
		Time:      now,
	}); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if err := store.AppendMessage(context.Background(), AppendMessageCommand{
		SessionID: "tx-1",
		MessageID: "m1",
		Time:      now.Add(time.Second),
		Role:      "user",
		Content:   []ContentBlock{{Type: "text", Text: "hello"}},
	}); err != nil {
		t.Fatalf("AppendMessage(): %v", err)
	}
	if err := store.AppendMessage(context.Background(), AppendMessageCommand{
		SessionID: "tx-1",
		MessageID: "m2",
		ParentID:  "m1",
		Time:      now.Add(2 * time.Second),
		Role:      "assistant",
		Content:   []ContentBlock{{Type: "text", Text: "world"}},
	}); err != nil {
		t.Fatalf("AppendMessage(m2): %v", err)
	}
	if err := store.Compact(context.Background(), CompactCommand{
		SessionID:  "tx-1",
		Time:       now.Add(3 * time.Second),
		BoundaryID: "m1",
	}); err != nil {
		t.Fatalf("Compact(): %v", err)
	}

	transcript, err := store.Load(context.Background(), "tx-1")
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if len(transcript.Messages) != 2 {
		t.Fatalf("expected 2 messages after compact (boundary=m1), got %d", len(transcript.Messages))
	}

	if err := store.Fork(context.Background(), ForkCommand{
		SourceSessionID: "tx-1",
		NewSessionID:    "tx-2",
		Time:            now.Add(4 * time.Second),
	}); err != nil {
		t.Fatalf("Fork(): %v", err)
	}
	forked, err := store.Load(context.Background(), "tx-2")
	if err != nil {
		t.Fatalf("Load(fork): %v", err)
	}
	if forked.ParentID != "tx-1" {
		t.Fatalf("fork ParentID = %q, want %q", forked.ParentID, "tx-1")
	}
}

// AppendMessage must be idempotent on the message ID and survive across
// FileStore instances (the second store reads the existing file fresh).
func TestFileStoreAppendMessageIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, "proj-1")
	if err != nil {
		t.Fatalf("NewFileStore(): %v", err)
	}

	now := time.Date(2026, 4, 6, 16, 20, 0, 0, time.UTC)
	if err := store.Start(context.Background(), StartCommand{
		SessionID: "tx-1", Cwd: "/tmp/project", Provider: "openai", Model: "gpt-test", Time: now,
	}); err != nil {
		t.Fatalf("Start(): %v", err)
	}

	msg := AppendMessageCommand{
		SessionID: "tx-1",
		MessageID: "m1",
		Time:      now.Add(time.Second),
		Role:      "user",
		Content:   []ContentBlock{{Type: "text", Text: "hello"}},
	}
	for i := range 3 {
		if err := store.AppendMessage(context.Background(), msg); err != nil {
			t.Fatalf("AppendMessage() #%d: %v", i, err)
		}
	}

	// A fresh store (empty cache) must still dedupe via the on-disk scan.
	fresh, err := NewFileStore(dir, "proj-1")
	if err != nil {
		t.Fatalf("NewFileStore() fresh: %v", err)
	}
	if err := fresh.AppendMessage(context.Background(), msg); err != nil {
		t.Fatalf("AppendMessage() fresh: %v", err)
	}

	tx, err := store.Load(context.Background(), "tx-1")
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if len(tx.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1 (dedup failed)", len(tx.Messages))
	}
}
