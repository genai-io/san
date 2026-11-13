package inspector

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/genai-io/san/internal/session"
)

func TestSidebarHonorsUserTitlesAndReportsDamagedAttrs(t *testing.T) {
	store, w, srv := fixture(t)
	appendPayload(t, w, session.SessionUpdatePayload{State: &session.StatePatch{Title: session.SetField("generated")}})
	if err := store.SetAttrs(context.Background(), w.ID(), session.AttrsPatch{Title: session.SetField("chosen")}); err != nil {
		t.Fatal(err)
	}
	before := diskSnapshot(t, store.ProjectDir())
	var items []sessionListItem
	getJSON(t, srv, "/api/sessions", &items)
	if len(items) != 1 || items[0].Title != "chosen" || items[0].AttrsError != "" {
		t.Fatalf("user title lost: %+v", items)
	}
	if !reflect.DeepEqual(before, diskSnapshot(t, store.ProjectDir())) {
		t.Fatal("attrs read modified files")
	}
	attrs := filepath.Join(store.ProjectDir(), "sessions", w.ID(), "attrs.json")
	if err := os.WriteFile(attrs, []byte("{damaged}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before = diskSnapshot(t, store.ProjectDir())
	getJSON(t, srv, "/api/sessions", &items)
	if items[0].AttrsError == "" {
		t.Fatal("damaged user intent silently treated as empty")
	}
	if !reflect.DeepEqual(before, diskSnapshot(t, store.ProjectDir())) {
		t.Fatal("damaged attrs repaired by read")
	}
	rawFixture(t, store, "new-with-title", `{"version":2,"events":[{"seq":1,"type":"future"}]}`)
	if err := store.SetAttrs(context.Background(), "new-with-title", session.AttrsPatch{Title: session.SetField("future title")}); err != nil {
		t.Fatal(err)
	}
	getJSON(t, srv, "/api/sessions", &items)
	found := false
	for _, item := range items {
		if item.ID == "new-with-title" {
			found = item.Title == "future title"
		}
	}
	if !found {
		t.Fatal("newer session title not browseable")
	}
}
