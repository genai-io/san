package session

import (
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"

	"os"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
)

// A Writer's session is fixed at construction, so forking has to get a new
// one. The live agent holds an onEvent closure over whichever Writer it was
// built with, which is why app.forkSession stops the agent: the rebuild is what
// picks the new Writer up. These pin the two halves of that contract.

func TestNewWriterRebindsAfterTheSessionIDChanges(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewStoreWithDir: %v", err)
	}
	setup := NewActive(store, "parent")

	parentRec := setup.NewWriter("main", "anthropic", "model", 1000)
	if parentRec == nil {
		t.Fatal("NewWriter returned nil for the parent session")
	}
	if got := parentRec.SessionID(); got != "parent" {
		t.Fatalf("parent writer session = %q, want %q", got, "parent")
	}

	// Forking re-points the Active at the new session.
	setup.SetID("fork")

	forkRec := setup.NewWriter("main", "anthropic", "model", 1000)
	if forkRec == nil {
		t.Fatal("NewWriter returned nil after the session changed")
	}
	if got := forkRec.SessionID(); got != "fork" {
		t.Errorf("writer session = %q, want %q — the cache did not invalidate", got, "fork")
	}
	if forkRec == parentRec {
		t.Error("the parent's Writer was reused; records would land in the parent transcript")
	}
}

func TestReturningToSessionKeepsEarlierMessages(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setup := NewActive(store, "original")
	rec := setup.NewWriter("main", "fake", "fake", 1000)
	msg := core.UserMessage("old conversation", nil)
	msg.ID = "old"
	rec.OnAgentEvent(sdkagent.MessageAdded{Message: msg})
	setup.SetID("other")
	setup.NewWriter("main", "fake", "fake", 1000)
	setup.SetID("original")
	rec = setup.NewWriter("main", "fake", "fake", 1000)
	msg = core.UserMessage("new turn", nil)
	msg.ID = "new"
	rec.OnAgentEvent(sdkagent.MessageAdded{Message: msg})
	snap, err := store.Load("original")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Messages) != 2 || snap.Messages[0].ID != "old" || snap.Messages[1].ID != "new" {
		t.Fatalf("history after returning = %+v", snap.Messages)
	}
}

// Active.Writer deliberately refuses a stale writer rather than letting a
// caller write into the wrong transcript. That guard is what the agent's
// captured closure bypasses, so it is worth pinning.
func TestSetupWriterRefusesAStaleWriter(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewStoreWithDir: %v", err)
	}
	setup := NewActive(store, "parent")

	if setup.NewWriter("main", "anthropic", "model", 1000) == nil {
		t.Fatal("NewWriter returned nil for the parent session")
	}
	if setup.Writer() == nil {
		t.Fatal("Writer() should return the writer bound to the current session")
	}

	setup.SetID("fork")

	if got := setup.Writer(); got != nil {
		t.Errorf("Writer() handed back a writer bound to %q while the session is %q",
			got.SessionID(), "fork")
	}
}

// Post-fork messages must land in the fork's transcript, not the parent's.
// This drives the writer through OnAgentEvent — the exact closure the live
// agent captures — and reads both transcripts back.
func TestForkWriterWritesToTheForkNotTheParent(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewStoreWithDir: %v", err)
	}
	setup := NewActive(store, "parent")
	parentRec := setup.NewWriter("main", "anthropic", "model", 1000)
	// The live agent stamps an id in append(); OnAppend drops messages without
	// one, so the harness has to do the same.
	parentRec.OnAgentEvent(sdkagent.MessageAdded{Message: core.Message{
		ID: "m1", Role: ai.RoleUser, Content: ai.TextContent("before the fork"),
	}})

	// Fork: the session id moves, and the rebuilt agent picks up a writer
	// bound to it. Using parentRec here instead is the bug.
	setup.SetID("fork")
	forkRec := setup.NewWriter("main", "anthropic", "model", 1000)
	forkRec.OnAgentEvent(sdkagent.MessageAdded{Message: core.Message{
		ID: "m2", Role: ai.RoleUser, Content: ai.TextContent("after the fork"),
	}})

	parentBody := readTranscript(t, store, "parent")
	forkBody := readTranscript(t, store, "fork")

	if !strings.Contains(forkBody, "after the fork") {
		t.Error("the post-fork message is missing from the fork transcript")
	}
	if strings.Contains(parentBody, "after the fork") {
		t.Error("the post-fork message landed in the parent transcript; " +
			"resuming the original would replay history from after the fork")
	}
	if !strings.Contains(parentBody, "before the fork") {
		t.Error("the parent lost its own pre-fork history")
	}
}

func readTranscript(t *testing.T, store *Store, sessionID string) string {
	t.Helper()
	data, err := os.ReadFile(store.transcriptStore.TranscriptPath(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read transcript %s: %v", sessionID, err)
	}
	return string(data)
}
