package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	sdksession "github.com/genai-io/sdk-go/pkg/session"

	"github.com/genai-io/san/internal/transcript"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestStoreSaveAndLoadPreservesCustomTitle(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore(): %v", err)
	}

	msgs := []ai.Message{ai.Message{Role: ai.RoleUser, Content: ai.TextContent("hello")}, ai.Message{Role: ai.RoleAssistant, Content: ai.TextContent("world")}}

	// Save with a custom title
	sess := &History{
		Metadata: Metadata{
			Title: "My Custom Session",
		},
		Messages: msgs,
	}
	if err := store.importHistory(sess); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	id := sess.Metadata.ID
	if id == "" {
		t.Fatal("Save did not assign session ID")
	}

	// Load and verify title
	loaded, err := store.Read(id)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if loaded.Metadata.Title != "My Custom Session" {
		t.Fatalf("Title = %q, want %q", loaded.Metadata.Title, "My Custom Session")
	}
}

func TestStoreSaveTitleChangePersists(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore(): %v", err)
	}

	msgs := []ai.Message{ai.Message{Role: ai.RoleUser, Content: ai.TextContent("hello")}}

	// Save with initial title
	sess := &History{
		Metadata: Metadata{
			Title: "Initial Title",
		},
		Messages: msgs,
	}
	if err := store.importHistory(sess); err != nil {
		t.Fatalf("Save() initial: %v", err)
	}

	id := sess.Metadata.ID

	// Rename: change the title and save again
	sess.Metadata.Title = "Renamed Title"
	if err := store.importHistory(sess); err != nil {
		t.Fatalf("Save() rename: %v", err)
	}

	// Load and verify new title
	loaded, err := store.Read(id)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if loaded.Metadata.Title != "Renamed Title" {
		t.Fatalf("Title = %q, want %q after rename", loaded.Metadata.Title, "Renamed Title")
	}
}

func TestStoreListPreservesCustomTitle(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore(): %v", err)
	}

	msgs := []ai.Message{ai.Message{Role: ai.RoleUser, Content: ai.TextContent("first message")}}

	sess := &History{
		Metadata: Metadata{
			Title: "Session From Listing",
		},
		Messages: msgs,
	}
	if err := store.importHistory(sess); err != nil {
		t.Fatalf("Save(): %v", err)
	}

	// List and verify title appears in listing
	list, err := store.List()
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(list) == 0 {
		t.Fatal("expected at least 1 session in listing")
	}
	found := false
	for _, meta := range list {
		if meta.Title == "Session From Listing" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected title %q in listing, got: %+v", "Session From Listing", list)
	}
}

func TestStoreRejectsUnsafeIDsWithoutTouchingAttachments(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PersistToolResult("safe", "call", "keep"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{".", "..", "../escape", "a/b", `a\b`} {
		t.Run(id, func(t *testing.T) {
			if opened, err := store.Create(CreateOptions{ID: id}); err == nil {
				_ = opened.Close()
				t.Fatal("created unsafe ID")
			}
			if opened, _, err := store.Open(id); err == nil {
				_ = opened.Close()
				t.Fatal("opened unsafe ID")
			}
			if _, err := store.Read(id); err == nil {
				t.Fatal("read unsafe ID")
			}
			if err := store.Delete(id); err == nil {
				t.Fatal("deleted unsafe ID")
			}
			if err := store.PersistToolResult(id, "call", "bad"); err == nil {
				t.Fatal("persisted unsafe session ID")
			}
			if err := store.PersistToolResult("safe", id, "bad"); err == nil {
				t.Fatal("persisted unsafe attachment ID")
			}
		})
	}
	data, err := os.ReadFile(filepath.Join(store.toolResultsDir("safe"), "call"))
	if err != nil || string(data) != "keep" {
		t.Fatalf("unrelated output changed: %q, %v", data, err)
	}
}

func TestDeleteRejectsLiveWriterAcrossStores(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStoreWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewStoreWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := store.Create(CreateOptions{ID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close() }()
	for _, deleting := range []*Store{store, other} {
		if err := deleting.Delete("s"); err == nil {
			t.Fatal("deleted active session")
		}
	}
	msg := ai.UserMessage("still writable")
	msg.ID = "m"
	if err := opened.Append(context.Background(), transcript.Record{Type: transcript.MessageAppended, Message: &msg}); err != nil {
		t.Fatal(err)
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
	if err := other.Delete("s"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read("s"); !errors.Is(err, sdksession.ErrNotFound) {
		t.Fatalf("deleted transcript remains: %v", err)
	}
}
