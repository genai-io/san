package inspector

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
	sdksession "github.com/genai-io/sdk-go/pkg/session"
)

func fixture(t *testing.T) (*session.Store, *session.Session, *httptest.Server) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := session.OpenStore(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	writer, err := store.Create(context.Background(), session.CreateOptions{ID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Close() })
	server := httptest.NewServer(New(store).Handler())
	t.Cleanup(server.Close)
	return store, writer, server
}
func message(id, text string) ai.Message {
	return ai.Message{ID: id, Role: ai.RoleUser, Content: ai.TextContent(text)}
}
func appendPayload(t *testing.T, s *session.Session, p session.EventPayload) {
	t.Helper()
	if err := s.Append(context.Background(), session.Event{Payload: p}); err != nil {
		t.Fatal(err)
	}
}
func getJSON(t *testing.T, s *httptest.Server, path string, out any) {
	t.Helper()
	r, err := s.Client().Get(s.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		b, _ := io.ReadAll(r.Body)
		t.Fatalf("%s: %d %s", path, r.StatusCode, b)
	}
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	if err = decoder.Decode(out); err != nil {
		t.Fatal(err)
	}
}

type diskFile struct {
	Data string
	Mod  time.Time
	Mode fs.FileMode
}

func diskSnapshot(t *testing.T, root string) map[string]diskFile {
	t.Helper()
	out := map[string]diskFile{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		out[path] = diskFile{string(b), info.ModTime(), info.Mode()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestServerNativeReadsDoNotModifyFilesOrClaimWriter(t *testing.T) {
	store, writer, srv := fixture(t)
	appendPayload(t, writer, session.MessageAppendPayload{Messages: []ai.Message{message("u", "hello")}})
	appendPayload(t, writer, session.SessionUpdatePayload{State: &session.StatePatch{Title: session.SetField("saved title")}})
	before := diskSnapshot(t, store.ProjectDir())
	var list []sessionListItem
	getJSON(t, srv, "/api/sessions", &list)
	if len(list) != 1 || list[0].ID != "session" || list[0].Title != "saved title" {
		t.Fatalf("list: %+v", list)
	}
	var records rawRecords
	getJSON(t, srv, "/api/sessions/session/records", &records)
	if len(records.Batches) != 3 || records.Error != "" {
		t.Fatalf("records: %+v", records)
	}
	var st nativeState
	getJSON(t, srv, "/api/sessions/session/state/0", &st)
	if len(st.Context.Messages) != 1 || st.Context.Messages[0].ID != "u" {
		t.Fatalf("state: %+v", st)
	}
	after := diskSnapshot(t, store.ProjectDir())
	if !reflect.DeepEqual(before, after) {
		t.Fatal("read modified files")
	}
	if err := writer.Append(context.Background(), session.Event{Payload: session.AnnotationAddPayload{Text: "still owns writer"}}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/sessions/..%2Fetc/records", "/api/sessions/session/state/nope"} {
		r, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != 404 {
			t.Fatalf("traversal status %d", r.StatusCode)
		}
	}
	r, err := srv.Client().Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(b), "/app.js") {
		t.Fatal("missing UI")
	}
}
func TestHTTPInferenceRequestsAndCompaction(t *testing.T) {
	store, w, srv := fixture(t)
	ctx := context.Background()
	appendPayload(t, w, session.InstructionsUpdatePayload{Set: map[string]session.InstructionSection{"base": {Content: "original", Slot: 2}}, Cause: "boot"})
	appendPayload(t, w, session.MessageAppendPayload{Messages: []ai.Message{message("old", "old")}})
	r, err := store.ContextRequest(ctx, w.ID(), 0)
	if err != nil {
		t.Fatal(err)
	}
	zero := 0.0
	r.Temperature = &zero
	r.MaxTokens = 81
	r.ToolChoice = ai.ToolChoiceNamed("native")
	r.SamplingParams = map[string]any{"seed": json.Number("9007199254740993")}
	h, err := w.StartInference(ctx, session.InferenceOptions{Purpose: session.PurposeCompaction, Provider: "test", Model: ai.Model{ID: "model"}, Request: r})
	if err != nil {
		t.Fatal(err)
	}
	appendPayload(t, w, session.MessageAppendPayload{Messages: []ai.Message{message("later", "later")}})
	if err = h.Finish(ctx, &ai.Response{}, nil, session.Event{Payload: session.ContextCompactPayload{Summary: message("summary", "summary")}}); err != nil {
		t.Fatal(err)
	}
	standalone := &ai.Request{System: "independent", Messages: []ai.Message{message("private", "not conversation")}, MaxTokens: 27, ToolChoice: ai.ToolChoiceNamed("native")}
	h2, err := w.StartInference(ctx, session.InferenceOptions{Purpose: session.PurposeJudge, Provider: "test", Model: ai.Model{ID: "judge"}, Request: standalone})
	if err != nil {
		t.Fatal(err)
	}
	if err = h2.Finish(ctx, &ai.Response{}, nil); err != nil {
		t.Fatal(err)
	}
	appendPayload(t, w, session.InstructionsUpdatePayload{Set: map[string]session.InstructionSection{"base": {Content: "changed"}}, Cause: "user:test"})
	for _, test := range []struct {
		seq  uint64
		want *ai.Request
	}{{h.Ref().Seq, r}, {h2.Ref().Seq, standalone}} {
		var st nativeState
		getJSON(t, srv, fmt.Sprintf("/api/sessions/session/state/%d", test.seq), &st)
		// Decode with UseNumber to compare native arbitrary JSON without float rounding.
		a, _ := json.Marshal(st.Request)
		b, _ := json.Marshal(test.want)
		var av, bv any
		da := json.NewDecoder(strings.NewReader(string(a)))
		da.UseNumber()
		da.Decode(&av)
		db := json.NewDecoder(strings.NewReader(string(b)))
		db.UseNumber()
		db.Decode(&bv)
		// The HTTP decoder above also needs UseNumber (getJSON supplies it).
		if !reflect.DeepEqual(av, bv) {
			t.Fatalf("historical request differs\ngot %s\nwant %s", a, b)
		}
	}
	var latest nativeState
	getJSON(t, srv, "/api/sessions/session/state/0", &latest)
	if latest.Context.System != "changed" || len(latest.Context.Messages) != 2 || latest.Context.Messages[0].ID != "summary" || latest.Context.Messages[1].ID != "later" {
		t.Fatalf("active: %+v", latest.Context)
	}
	found := false
	for _, m := range latest.History {
		if m.ID == "old" {
			found = true
		}
	}
	if !found {
		t.Fatal("pre-compaction history lost")
	}
}

// A real SDK writer creates newer/invalid domain payloads without implementing framing here.
func rawFixture(t *testing.T, store *session.Store, id, data string) {
	t.Helper()
	dir := filepath.Join(store.ProjectDir(), "sessions", id)
	sdk, err := sdksession.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	w, err := sdk.Writer(context.Background(), "events")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	_, err = w.Commit(context.Background(), sdksession.Batch{ID: "raw", Records: []sdksession.Record{{Type: sdksession.TypeApp, Name: "san.events", Time: time.UnixMilli(1000).UTC(), Data: json.RawMessage(data)}}})
	if err != nil {
		t.Fatal(err)
	}
}
func TestUnsupportedAndCorruptRemainVisible(t *testing.T) {
	store, _, srv := fixture(t)
	for _, test := range []struct{ id, data string }{{"new", `{"version":2,"events":[{"seq":1,"type":"future.event","v":9,"payload":{"text":"<script>bad</script>"}}]}`}, {"invalid", `{"version":1,"events":[{"seq":1,"type":"session.create","v":1,"time":1000,"payload":{}}],"validation":{}}`}} {
		rawFixture(t, store, test.id, test.data)
		var out rawRecords
		getJSON(t, srv, "/api/sessions/"+test.id+"/records", &out)
		if len(out.Batches) != 1 {
			t.Fatal("raw batch hidden")
		}
		raw, _ := json.Marshal(out.Batches[0])
		if !strings.Contains(string(raw), "san.events") {
			t.Fatal("lost native raw")
		}
		resp, err := srv.Client().Get(srv.URL + "/api/sessions/" + test.id + "/state/1")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 422 {
			t.Fatal("invalid native projection fabricated")
		}
	}
	dir := filepath.Join(store.ProjectDir(), "sessions", "broken")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(path, []byte("{broken}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before := diskSnapshot(t, store.ProjectDir())
	var out rawRecords
	getJSON(t, srv, "/api/sessions/broken/records", &out)
	if out.Error == "" || out.Unvalidated != "{broken}\n" {
		t.Fatalf("missing corrupt diagnosis: %+v", out)
	}
	var list []sessionListItem
	getJSON(t, srv, "/api/sessions", &list)
	if len(list) != 4 {
		t.Fatalf("unreadable sessions hidden: %+v", list)
	}
	if !reflect.DeepEqual(before, diskSnapshot(t, store.ProjectDir())) {
		t.Fatal("raw read modified file")
	}
}
func TestStreamPublishesWholeCommittedBatchesInOrder(t *testing.T) {
	_, w, srv := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/sessions/session/stream", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	scan := bufio.NewScanner(resp.Body)
	next := func() rawBatch {
		t.Helper()
		for scan.Scan() {
			line := scan.Text()
			if strings.HasPrefix(line, "data: ") {
				var b rawBatch
				if e := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &b); e != nil {
					t.Fatal(e)
				}
				return b
			}
		}
		t.Fatalf("stream ended: %v", scan.Err())
		return rawBatch{}
	}
	first := next()
	if first.Tail.Sequence != 1 {
		t.Fatal("missing creation")
	}
	if err = w.Append(context.Background(), session.Event{Payload: session.AnnotationAddPayload{Text: "one"}}, session.Event{Payload: session.AnnotationAddPayload{Text: "two"}}); err != nil {
		t.Fatal(err)
	}
	second := next()
	if second.Tail.Sequence != 2 || second.Batch.Expected != first.Tail {
		t.Fatal("commit order changed")
	}
	var payload struct {
		Events []session.Event `json:"events"`
	}
	if err = json.Unmarshal(second.Batch.Records[0].Data, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 2 || payload.Events[0].Seq != 2 || payload.Events[1].Seq != 3 {
		t.Fatal("partial or reordered batch")
	}
}
