package trace

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		`{"id":"sess-1:start","sessionId":"sess-1","time":"2026-05-15T00:00:00Z","type":"session.started","session":{"provider":"p","model":"m"}}`,
		`{"id":"sess-1:m1","sessionId":"sess-1","time":"2026-05-15T00:00:01Z","type":"message.appended","message":{"messageId":"m1","role":"user","content":[{"type":"text","text":"hello"}]}}`,
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
