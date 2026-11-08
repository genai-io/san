package inspector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/transcript"
	"github.com/genai-io/sdk-go/pkg/ai"
	sdksession "github.com/genai-io/sdk-go/pkg/session"
)

type eventReaderFunc func(context.Context, string, session.EventQuery) (session.EventPage, error)

func (f eventReaderFunc) Events(ctx context.Context, id string, q session.EventQuery) (session.EventPage, error) {
	return f(ctx, id, q)
}

func TestEventEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"legacy", fmt.Errorf("wrapped: %w", sdksession.ErrMode), 409, "legacy_format"},
		{"corrupt", errors.New("invalid version"), 422, "invalid_log"},
		{"wrong payload", transcript.ErrEventFormat, 422, "invalid_log"},
		{"cursor", session.ErrInvalidEventQuery, 400, "invalid_query"},
		{"missing", sdksession.ErrNotFound, 404, "not_found"},
		{"cancelled", context.Canceled, 408, "cancelled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewWithEventReader(t.TempDir(), eventReaderFunc(func(context.Context, string, session.EventQuery) (session.EventPage, error) {
				return session.EventPage{}, tc.err
			}))
			defer s.Close()
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/sessions/s/events", nil))
			var body struct{ Code string }
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			if w.Code != tc.status || body.Code != tc.code {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	s := NewWithEventReader(t.TempDir(), eventReaderFunc(func(got context.Context, id string, q session.EventQuery) (session.EventPage, error) {
		calls++
		if got != ctx || id != "s" || q.Limit != 2 || q.Cursor != "opaque" {
			t.Fatalf("forwarded %v %s %+v", got, id, q)
		}
		return session.EventPage{Events: []session.Event{}, NextCursor: "next", ThroughSeq: 7}, nil
	}))
	defer s.Close()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/sessions/s/events?limit=2&cursor=opaque", nil).WithContext(ctx))
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"throughSeq":7`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"nextCursor":"next"`)) {
		t.Fatal(w.Body)
	}
	for _, path := range []string{"/api/sessions/s/events?limit=0", "/api/sessions/s/events?limit=501", "/api/sessions/s/events?limit=no", "/api/sessions/s/events?limit="} {
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/sessions/s/events", nil))
	if w.Code != 405 || calls != 1 {
		t.Fatalf("%d calls %d", w.Code, calls)
	}
}

func TestEventEndpointNativeNumberDetail(t *testing.T) {
	event := session.Event{ID: "schema", Seq: 2, Type: session.EventToolsetUpdate, Payload: session.ToolsetUpdatePayload{Upsert: []ai.Schema{{Name: "Exact", Definition: map[string]any{"const": json.Number("9007199254740993")}}}}}
	s := NewWithEventReader(t.TempDir(), eventReaderFunc(func(context.Context, string, session.EventQuery) (session.EventPage, error) {
		return session.EventPage{Events: []session.Event{event}, ThroughSeq: 2}, nil
	}))
	defer s.Close()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/sessions/s/events", nil))
	var body struct {
		Events []struct {
			ID   string `json:"id"`
			JSON string `json:"json"`
		} `json:"events"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(body.Events) != 1 || body.Events[0].ID != "schema" || !bytes.Contains([]byte(body.Events[0].JSON), []byte(`"const": 9007199254740993`)) {
		t.Fatalf("native detail lost precision: %d %s", w.Code, w.Body)
	}
}

func TestEventEndpointRealLogDoesNotClaimOrRepair(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	reader, err := session.NewStoreWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	writer, err := transcript.NewStore(dir, session.ProjectID(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	release, err := writer.ClaimContext(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	event := session.Event{ID: "create", SessionID: "s", Seq: 1, Type: session.EventSessionCreate, SchemaVersion: 1, CreatedAt: 123, Payload: session.SessionCreatePayload{ProjectID: session.ProjectID(dir), Workspace: dir, InitialCWD: dir, AgentID: "main"}}
	annotation := session.Event{ID: "note", SessionID: "s", Seq: 2, Type: session.EventAnnotationAdd, SchemaVersion: 1, CreatedAt: 123, Payload: session.AnnotationAddPayload{Text: "note"}}
	payload, err := json.Marshal(struct {
		Version    int                            `json:"version"`
		Events     []session.Event                `json:"events"`
		Validation session.EventValidationContext `json:"validation"`
	}{1, []session.Event{event, annotation}, session.EventValidationContext{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = writer.CommitBatch(ctx, "s", sdksession.Batch{ID: "b", Records: []sdksession.Record{{Type: sdksession.TypeApp, Name: "san.events", Time: time.UnixMilli(123).UTC(), Data: payload}}})
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithEventReader(dir, reader)
	defer s.Close()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/sessions/s/events", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	prefix, err := os.ReadFile(reader.TranscriptPath("s"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := reader.Events(ctx, "s", session.EventQuery{Limit: 1})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("page %+v: %v", first, err)
	}
	withoutReader := New(dir)
	defer withoutReader.Close()
	unsupported := httptest.NewRecorder()
	withoutReader.Handler().ServeHTTP(unsupported, httptest.NewRequest("GET", "/api/sessions/s/events", nil))
	if unsupported.Code != 503 {
		t.Fatalf("typed without reader: %d %s", unsupported.Code, unsupported.Body)
	}
	f, err := os.OpenFile(reader.TranscriptPath("s"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"incomplete":`)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(reader.TranscriptPath("s"))
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/sessions/s/events", nil))
	if w.Code != 200 {
		t.Fatalf("partial typed log unreadable: %d %s", w.Code, w.Body)
	}
	after, err := os.ReadFile(reader.TranscriptPath("s"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("reader changed log")
	}
	legacy := []byte("{\"type\":\"app\",\"name\":\"session.started\",\"data\":{}}\n")
	mixed := append(bytes.Clone(prefix), legacy...)
	if err := os.WriteFile(reader.TranscriptPath("s"), mixed, 0o600); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/sessions/s/events", nil))
	if w.Code != 422 || !bytes.Contains(w.Body.Bytes(), []byte(`"code":"invalid_log"`)) {
		t.Fatalf("mixed format: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/sessions/s/events?cursor="+first.NextCursor, nil))
	if w.Code != 200 {
		t.Fatalf("pinned continuation: %d %s", w.Code, w.Body)
	}
	after, err = os.ReadFile(reader.TranscriptPath("s"))
	if err != nil || !bytes.Equal(mixed, after) {
		t.Fatalf("mixed read changed bytes: %v", err)
	}
	if err := os.WriteFile(reader.TranscriptPath("legacy"), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	withoutReader.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/sessions/legacy/events", nil))
	if w.Code != 409 || !bytes.Contains(w.Body.Bytes(), []byte(`"code":"legacy_format"`)) {
		t.Fatalf("legacy constructor: %d %s", w.Code, w.Body)
	}
}
