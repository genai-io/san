package inspector

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/genai-io/san/internal/transcript"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// writeSession writes records after a session.started into projectDir's
// session "sess-1". Record IDs are positions in the log, start being "0".
func writeSession(t *testing.T, projectDir string, records ...transcript.Record) {
	t.Helper()
	store, err := transcript.NewStore(projectDir, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Start(ctx, transcript.StartCommand{SessionID: "sess-1", Provider: "p", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	for _, rec := range records {
		rec.SessionID = "sess-1"
		if err := store.Append(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
}

func user(id, text string) transcript.Record {
	return transcript.Record{Type: transcript.MessageAppended, Message: &ai.Message{ID: id, Role: ai.RoleUser, Content: ai.TextContent(text)}}
}

// End-to-end smoke: write a transcript file under projectDir/transcripts, hit
// the public API, and verify the listing + records endpoints reflect what's
// on disk. Also catches the path-traversal guard.
func TestServerListAndRecords(t *testing.T) {
	projectDir := t.TempDir()
	writeSession(t, projectDir, user("m1", "hello"))
	body, err := os.ReadFile(filepath.Join(projectDir, "transcripts", "sess-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(New(projectDir).Handler())
	defer srv.Close()

	// /api/sessions returns the one transcript with title pulled from the
	// first user message text.
	resp, err := srv.Client().Get(srv.URL + "/api/sessions")
	if err != nil {
		t.Fatalf("GET /api/sessions: %v", err)
	}
	defer resp.Body.Close()
	var list []sessionListItem
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].ID != "sess-1" {
		t.Fatalf("unexpected session list: %+v", list)
	}
	if list[0].Title != "hello" {
		t.Fatalf("Title = %q, want %q", list[0].Title, "hello")
	}

	// /api/sessions/sess-1/records?after=0 returns the full body and a
	// next-offset header equal to the file size.
	resp2, err := srv.Client().Get(srv.URL + "/api/sessions/sess-1/records?after=0")
	if err != nil {
		t.Fatalf("GET records: %v", err)
	}
	defer resp2.Body.Close()
	if got := resp2.Header.Get("X-Next-Offset"); got != "" {
		// Must equal len(body) since the file is fully line-terminated.
		if parsed, _ := json.Number(got).Int64(); parsed != int64(len(body)) {
			t.Fatalf("X-Next-Offset = %s, want %d", got, len(body))
		}
	}

	// Path traversal must be rejected even with URL-encoded slashes.
	resp3, err := srv.Client().Get(srv.URL + "/api/sessions/..%2Fetc/records")
	if err != nil {
		t.Fatalf("GET traversal: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != 404 {
		t.Fatalf("traversal status = %d, want 404", resp3.StatusCode)
	}
}

// An empty project directory must yield an empty list, not an error.
func TestServerListNoTranscripts(t *testing.T) {
	projectDir := t.TempDir()

	srv := httptest.NewServer(New(projectDir).Handler())
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/api/sessions")
	if err != nil {
		t.Fatalf("GET /api/sessions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var list []sessionListItem
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %+v", list)
	}
}

func TestServerStateReplaysContextAtRecord(t *testing.T) {
	projectDir := t.TempDir()
	writeSession(t, projectDir,
		transcript.Record{Type: transcript.SystemSectionAdded, System: &transcript.SystemSectionRecord{Name: "identity", Content: "You are precise.", Caller: "system:init"}},
		transcript.Record{Type: transcript.ToolAdded, Tool: &transcript.ToolRecord{Schema: &ai.Schema{Name: "Read", Description: "read", Definition: json.RawMessage(`{"type":"object"}`)}, Caller: "tools:init"}},
		user("m1", "hello"),
		transcript.Record{Type: transcript.InferenceRequested, Inference: &transcript.InferenceRecord{Sequence: 1, SystemDigest: "bad", ToolsDigest: "bad", MessageIDs: []string{"m1"}}},
	)

	srv := httptest.NewServer(New(projectDir).Handler())
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/api/sessions/sess-1/state/4")
	if err != nil {
		t.Fatalf("GET state: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var state replayState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state.Provider != "p" || state.Model != "m" {
		t.Fatalf("model state = %+v", state)
	}
	if len(state.System) != 1 || state.System[0].Content != "You are precise." {
		t.Fatalf("system state = %+v", state.System)
	}
	if len(state.Tools) != 1 || state.Tools[0].Name != "Read" {
		t.Fatalf("tools state = %+v", state.Tools)
	}
	if len(state.Messages) != 1 || state.Messages[0].ID != "m1" {
		t.Fatalf("messages state = %+v", state.Messages)
	}
	if len(state.Integrity) != 3 || state.Integrity[0].OK || state.Integrity[1].OK || !state.Integrity[2].OK {
		t.Fatalf("integrity should report digest mismatches: %+v", state.Integrity)
	}
}

func TestServerStateUsesTheConversationAfterAReplacement(t *testing.T) {
	projectDir := t.TempDir()
	writeSession(t, projectDir,
		user("m1", "root"),
		user("m2", "old branch"),
		transcript.Record{Type: transcript.ConversationReplaced, Messages: []ai.Message{
			{ID: "m1", Role: ai.RoleUser, Content: ai.TextContent("root")},
			{ID: "m3", Role: ai.RoleAssistant, Content: ai.TextContent("active branch")},
		}},
		transcript.Record{Type: transcript.InferenceRequested, Inference: &transcript.InferenceRecord{Sequence: 1, MessageIDs: []string{"m1", "m3"}}},
	)

	srv := httptest.NewServer(New(projectDir).Handler())
	defer srv.Close()

	// start, m1, m2, truncate to [m1], m3, inference
	resp, err := srv.Client().Get(srv.URL + "/api/sessions/sess-1/state/5")
	if err != nil {
		t.Fatalf("GET state: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var state replayState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if len(state.Messages) != 2 || state.Messages[0].ID != "m1" || state.Messages[1].ID != "m3" {
		t.Fatalf("messages state = %+v, want [m1 m3]", state.Messages)
	}
	if len(state.Messages[1].Content) != 1 || state.Messages[1].Content[0].Text != "active branch" {
		t.Fatalf("message content not exposed for context review: %+v", state.Messages[1])
	}
	if len(state.Integrity) != 3 || !state.Integrity[2].OK {
		t.Fatalf("messageIds integrity = %+v, want OK", state.Integrity)
	}
}

func TestServerStateReportsMessageIDMismatch(t *testing.T) {
	projectDir := t.TempDir()
	writeSession(t, projectDir, user("m1", "hello"),
		transcript.Record{Type: transcript.InferenceRequested, Inference: &transcript.InferenceRecord{Sequence: 1, MessageIDs: []string{"missing"}}})

	srv := httptest.NewServer(New(projectDir).Handler())
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/api/sessions/sess-1/state/2")
	if err != nil {
		t.Fatalf("GET state: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var state replayState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if len(state.Integrity) != 3 || state.Integrity[2].Field != "messageIds" || state.Integrity[2].OK {
		t.Fatalf("expected messageIds mismatch, got %+v", state.Integrity)
	}
}
