package inspector

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/transcript"
)

// End-to-end smoke: write a transcript file under projectDir/transcripts, hit
// the public API, and verify the listing + records endpoints reflect what's
// on disk. Also catches the path-traversal guard.
func TestServerListAndRecords(t *testing.T) {
	projectDir := t.TempDir()
	txDir := filepath.Join(projectDir, "transcripts")
	if err := os.MkdirAll(txDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := strings.Join([]string{
		`{"id":"sess-1:start","sessionId":"sess-1","time":"2026-05-15T00:00:00Z","type":"session.started","version":"` + transcript.SchemaVersion + `","start":{"provider":"p","model":"m"}}`,
		`{"id":"sess-1:m1","sessionId":"sess-1","time":"2026-05-15T00:00:01Z","type":"message.appended","message":{"id":"m1","role":"user","content":[{"type":"text","text":"hello"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(txDir, "sess-1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
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
	txDir := filepath.Join(projectDir, "transcripts")
	if err := os.MkdirAll(txDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := strings.Join([]string{
		`{"id":"sess-1:start","sessionId":"sess-1","time":"2026-05-15T00:00:00Z","type":"session.started","version":"` + transcript.SchemaVersion + `","start":{"provider":"p","model":"m"}}`,
		`{"id":"sess-1:system:1","sessionId":"sess-1","time":"2026-05-15T00:00:01Z","type":"system.section.added","version":"` + transcript.SchemaVersion + `","system":{"name":"identity","slot":0,"content":"You are precise.","caller":"system:init"}}`,
		`{"id":"sess-1:tool:1","sessionId":"sess-1","time":"2026-05-15T00:00:02Z","type":"tool.added","version":"` + transcript.SchemaVersion + `","tool":{"schema":{"name":"Read","description":"read","definition":{"type":"object"}},"caller":"tools:init"}}`,
		`{"id":"sess-1:m1","sessionId":"sess-1","time":"2026-05-15T00:00:03Z","type":"message.appended","version":"` + transcript.SchemaVersion + `","message":{"id":"m1","role":"user","content":[{"type":"text","text":"hello"}]}}`,
		`{"id":"sess-1:infer:1","sessionId":"sess-1","time":"2026-05-15T00:00:04Z","type":"inference.requested","version":"` + transcript.SchemaVersion + `","inference":{"sequence":1,"systemDigest":"bad","toolsDigest":"bad","messageIds":["m1"]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(txDir, "sess-1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	srv := httptest.NewServer(New(projectDir).Handler())
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/api/sessions/sess-1/state/sess-1:infer:1")
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

func TestServerStateUsesActiveMessageChain(t *testing.T) {
	projectDir := t.TempDir()
	txDir := filepath.Join(projectDir, "transcripts")
	if err := os.MkdirAll(txDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := strings.Join([]string{
		`{"id":"sess-1:start","sessionId":"sess-1","time":"2026-05-15T00:00:00Z","type":"session.started","version":"` + transcript.SchemaVersion + `","start":{"provider":"p","model":"m"}}`,
		`{"id":"sess-1:m1","sessionId":"sess-1","time":"2026-05-15T00:00:01Z","type":"message.appended","version":"` + transcript.SchemaVersion + `","message":{"id":"m1","role":"user","content":[{"type":"text","text":"root"}]}}`,
		`{"id":"sess-1:m2","sessionId":"sess-1","time":"2026-05-15T00:00:02Z","type":"message.appended","version":"` + transcript.SchemaVersion + `","parentMessageId":"m1","message":{"id":"m2","role":"assistant","content":[{"type":"text","text":"old branch"}]}}`,
		`{"id":"sess-1:m3","sessionId":"sess-1","time":"2026-05-15T00:00:03Z","type":"message.appended","version":"` + transcript.SchemaVersion + `","parentMessageId":"m1","message":{"id":"m3","role":"assistant","content":[{"type":"text","text":"active branch"}]}}`,
		`{"id":"sess-1:infer:1","sessionId":"sess-1","time":"2026-05-15T00:00:04Z","type":"inference.requested","version":"` + transcript.SchemaVersion + `","inference":{"sequence":1,"messageIds":["m1","m3"]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(txDir, "sess-1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	srv := httptest.NewServer(New(projectDir).Handler())
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/api/sessions/sess-1/state/sess-1:infer:1")
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
		t.Fatalf("messages state = %+v, want active chain [m1 m3]", state.Messages)
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
	txDir := filepath.Join(projectDir, "transcripts")
	if err := os.MkdirAll(txDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := strings.Join([]string{
		`{"id":"sess-1:start","sessionId":"sess-1","time":"2026-05-15T00:00:00Z","type":"session.started","version":"` + transcript.SchemaVersion + `","start":{"provider":"p","model":"m"}}`,
		`{"id":"sess-1:m1","sessionId":"sess-1","time":"2026-05-15T00:00:01Z","type":"message.appended","version":"` + transcript.SchemaVersion + `","message":{"id":"m1","role":"user","content":[{"type":"text","text":"hello"}]}}`,
		`{"id":"sess-1:infer:1","sessionId":"sess-1","time":"2026-05-15T00:00:02Z","type":"inference.requested","version":"` + transcript.SchemaVersion + `","inference":{"sequence":1,"messageIds":["missing"]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(txDir, "sess-1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	srv := httptest.NewServer(New(projectDir).Handler())
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/api/sessions/sess-1/state/sess-1:infer:1")
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
