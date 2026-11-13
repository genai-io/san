package inspector

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/session"
)

func TestReadsNeverServeStaleStateAfterReplacement(t *testing.T) {
	store, w, srv := fixture(t)
	appendPayload(t, w, session.InstructionsUpdatePayload{Set: map[string]session.InstructionSection{"system": {Content: "old"}}, Cause: "boot"})
	var st nativeState
	getJSON(t, srv, "/api/sessions/session/state/0", &st)
	if st.Context.System != "old" {
		t.Fatal("fixture")
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(context.Background(), w.ID()); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	New(store).Handler().ServeHTTP(response, httptest.NewRequest("GET", "/api/sessions/session/state/0", nil))
	if response.Code == 200 {
		t.Fatal("deleted session served stale state")
	}
	replacement, err := store.Create(context.Background(), session.CreateOptions{ID: w.ID()})
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	appendPayload(t, replacement, session.InstructionsUpdatePayload{Set: map[string]session.InstructionSection{"system": {Content: "replacement"}}, Cause: "boot"})
	getJSON(t, srv, "/api/sessions/session/state/0", &st)
	if st.Context.System != "replacement" {
		t.Fatal("replacement served stale state")
	}
}

func TestIncompleteSDKBatchNeverAppearsInRecords(t *testing.T) {
	store, w, srv := fixture(t)
	original, err := os.ReadFile(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	appendPayload(t, w, session.AnnotationAddPayload{Text: "not yet complete"})
	complete, err := os.ReadFile(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	// Cut a genuine SDK envelope before its terminating newline. SDK determines
	// commit visibility; inspector must not infer it from parseable event content.
	if err = os.WriteFile(w.Path(), complete[:len(complete)-2], 0600); err != nil {
		t.Fatal(err)
	}
	var records rawRecords
	getJSON(t, srv, "/api/sessions/session/records", &records)
	if len(records.Batches) != 1 {
		t.Fatalf("incomplete batch exposed: %+v", records)
	}
	before := diskSnapshot(t, store.ProjectDir())
	getJSON(t, srv, "/api/sessions/session/records", &records)
	if !reflect.DeepEqual(before, diskSnapshot(t, store.ProjectDir())) {
		t.Fatal("incomplete tail repaired by reader")
	}
	if err = os.WriteFile(w.Path(), complete, 0600); err != nil {
		t.Fatal(err)
	}
	getJSON(t, srv, "/api/sessions/session/records", &records)
	if len(records.Batches) != 2 {
		t.Fatal("completed batch missing")
	}
	raw, _ := json.Marshal(records)
	if !strings.Contains(string(raw), "not yet complete") {
		t.Fatal("payload lost")
	}
	if len(before) == 0 || len(original) == 0 {
		t.Fatal("empty fixture")
	}
}
