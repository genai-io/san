package session

import (
	"testing"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestResumingSameSessionRecoversABusyWriter(t *testing.T) {
	dir := t.TempDir()
	a, err := NewStoreWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewStoreWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	other := NewActive(a, "s")
	defer other.Close()
	other.NewWriter("main", "p", "m", 100)
	active := NewActive(b, "s")
	defer active.Close()
	failed := active.NewWriter("main", "p", "m", 100)
	if failed.Err() == nil {
		t.Fatal("competing writer unexpectedly acquired the lease")
	}
	before := readTranscript(t, a, "s")
	failed.RecordNotice("no lease", false)
	if after := readTranscript(t, a, "s"); after != before {
		t.Fatal("failed writer appended audit events without owning the lease")
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := active.Resume("s"); err != nil {
		t.Fatal(err)
	}
	w := active.NewWriter("main", "p", "m", 100)
	if w == failed || w.Err() != nil {
		t.Fatalf("same-ID resume retained the failed writer: %v", w.Err())
	}
	w.OnAgentEvent(sdkagent.MessageAdded{Message: ai.Message{ID: "retry", Role: ai.RoleUser, Content: ai.TextContent("retry")}})
	loaded, err := b.Load("s")
	if err != nil || len(loaded.Messages) != 1 || loaded.Messages[0].ID != "retry" {
		t.Fatalf("recovered writer failed to persist: %+v, %v", loaded, err)
	}
}

func TestResumeReadsCurrentHistoryUnderTheWriterLease(t *testing.T) {
	dir := t.TempDir()
	a, err := NewStoreWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewStoreWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	owner := NewActive(a, "s")
	defer owner.Close()
	w := owner.NewWriter("main", "p", "m", 100)
	appendMessage := func(w *Writer, id string) {
		w.OnAgentEvent(sdkagent.MessageAdded{Message: ai.Message{ID: id, Role: ai.RoleUser, Content: ai.TextContent(id)}})
	}
	appendMessage(w, "m1")
	// A selector can read an older snapshot while another process is writing.
	if _, err := b.Load("s"); err != nil {
		t.Fatal(err)
	}
	active := NewActive(b, "other")
	defer active.Close()
	if _, err := active.Resume("s"); err == nil || active.ID() != "other" {
		t.Fatal("resume accepted a busy session or replaced the current identity")
	}
	appendMessage(w, "m2")
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := active.Resume("s")
	if err != nil || len(resumed.Messages) != 2 || resumed.Messages[1].ID != "m2" {
		t.Fatalf("resume used the stale selector snapshot: %+v, %v", resumed, err)
	}
	// The resumed session owns the lease before its lazy runtime starts.
	if _, err := owner.Resume("s"); err == nil {
		t.Fatal("idle resumed session released its writer lease")
	}
	w = active.NewWriter("main", "p", "m", 100)
	w.OnAgentEvent(sdkagent.MessagesReplaced{Messages: resumed.Messages})
	appendMessage(w, "m3")
	loaded, err := b.Load("s")
	if err != nil || len(loaded.Messages) != 3 || loaded.Messages[1].ID != "m2" {
		t.Fatalf("resume handoff lost intervening history: %+v, %v", loaded, err)
	}
}

func TestResumeMissingSessionDoesNotCreateATranscript(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	active := NewActive(store, "current")
	defer active.Close()
	if _, err := active.Resume("missing"); err == nil {
		t.Fatal("missing session resumed successfully")
	}
	if items, err := store.List(); err != nil || len(items) != 0 || active.ID() != "current" {
		t.Fatalf("failed resume created a session: %+v, %v", items, err)
	}
}

func TestHydratedResumeDoesNotRewriteNativeHistory(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const preview = "preview\n\n[Full output persisted to blobs/tool-result/s/c]"
	snapshot := &Snapshot{Metadata: Metadata{ID: "s"}, Messages: []ai.Message{
		{ID: "call", Role: ai.RoleAssistant, Content: ai.Content{ai.ToolCallBlock(ai.ToolCall{ID: "c", Name: "Bash", Input: `{}`})}},
		{ID: "result", Role: ai.RoleUser, Content: ai.Content{ai.ToolResultBlock(ai.ToolResult{ToolCallID: "c", ToolName: "Bash", Content: ai.TextContent(preview)})}},
	}}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := store.PersistToolResult("s", "c", "full output"); err != nil {
		t.Fatal(err)
	}
	active := NewActive(store, "s")
	defer active.Close()
	resumed, err := active.Resume("s")
	if err != nil || resumed.Messages[1].ToolResults()[0].Content.Text() != "full output" {
		t.Fatalf("resume did not hydrate the blob: %+v, %v", resumed, err)
	}
	before := readTranscript(t, store, "s")
	active.Writer().OnAgentEvent(sdkagent.MessagesReplaced{Messages: resumed.Messages})
	if after := readTranscript(t, store, "s"); before != after {
		t.Fatal("hydrated resume copied or replaced the canonical transcript")
	}
}
