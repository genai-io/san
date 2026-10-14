package inspector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/transcript"
)

func TestReplayIntegrityBelongsToSelectedInference(t *testing.T) {
	var b replayBuilder
	b.apply(transcript.Record{ID: "bad", Type: transcript.InferenceRequested, Inference: &transcript.InferenceRecord{SystemDigest: "wrong"}})
	if b.snapshot().Integrity[0].OK {
		t.Fatal("bad inference should fail integrity")
	}
	b.apply(transcript.Record{ID: "good", Type: transcript.InferenceRequested, Inference: &transcript.InferenceRecord{SystemDigest: transcript.DigestSystem("")}})
	checks := b.snapshot().Integrity
	if len(checks) != 3 {
		t.Fatalf("selected inference inherited previous checks: %+v", checks)
	}
	for _, check := range checks {
		if !check.OK {
			t.Fatalf("healthy inference inherited a failure: %+v", check)
		}
	}
}

func TestReplayCacheDoesNotOutliveTranscript(t *testing.T) {
	s := New(t.TempDir())
	ctx := context.Background()
	if err := s.store.Start(ctx, transcript.StartCommand{SessionID: "s", Model: "original"}); err != nil {
		t.Fatal(err)
	}
	get := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/sessions/s/state/s:start", nil))
		return w
	}
	if got := get(); got.Code != http.StatusOK {
		t.Fatalf("initial replay: %s", got.Body.String())
	}
	if err := s.store.Delete(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	if got := get(); got.Code != http.StatusNotFound {
		t.Fatalf("deleted transcript still served from cache: %d %s", got.Code, got.Body.String())
	}
	if err := s.store.Start(ctx, transcript.StartCommand{SessionID: "s", Model: "replacement"}); err != nil {
		t.Fatal(err)
	}
	if got := get(); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "replacement") {
		t.Fatalf("replacement transcript served stale content: %s", got.Body.String())
	}
}

func TestInspectorKeepsUnreadableFilesAvailableForDiagnosis(t *testing.T) {
	s := New(t.TempDir())
	ctx := context.Background()
	if err := s.store.Start(ctx, transcript.StartCommand{SessionID: "healthy"}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.Append(ctx, transcript.Record{SessionID: "healthy", Type: transcript.SessionStatePatched, State: &transcript.StatePatch{Title: new("saved title")}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.store.TranscriptPath("broken"), []byte("{broken}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	var items []sessionListItem
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil || len(items) != 2 {
		t.Fatalf("raw transcript missing from inspector: %s, %v", w.Body.String(), err)
	}
	for _, item := range items {
		if item.ID == "healthy" && item.Title != "saved title" {
			t.Fatal("inspector lost the saved title")
		}
	}
}

func TestReadFromOffsetIncludesCompleteUnterminatedRecord(t *testing.T) {
	path := t.TempDir() + "/s.jsonl"
	for _, body := range []string{`{"id":"complete"}`, "{\"id\":\"first\"}\n{\"id\":\"complete\"}"} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		data, offset, err := readFromOffset(path, 0)
		if err != nil || string(data) != body+"\n" || offset != int64(len(body)) {
			t.Fatalf("complete EOF record was hidden: data=%q offset=%d err=%v", data, offset, err)
		}
	}
	if err := os.WriteFile(path, []byte("{\"id\":\"first\"}\n{\"id\":"), 0600); err != nil {
		t.Fatal(err)
	}
	data, offset, err := readFromOffset(path, 0)
	if err != nil || string(data) != "{\"id\":\"first\"}\n" || offset != int64(len(data)) {
		t.Fatalf("incomplete EOF record must wait for more bytes: data=%q offset=%d err=%v", data, offset, err)
	}
}
