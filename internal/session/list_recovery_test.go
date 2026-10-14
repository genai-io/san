package session

import (
	"os"
	"testing"
)

func TestBadTranscriptDoesNotHideHealthySessions(t *testing.T) {
	for _, bad := range []string{"{\"type\":\"session.started\",\"version\":\"1\"}\n", "{broken}\n"} {
		store, err := NewStoreWithDir(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		opened, err := store.Create(CreateOptions{ID: "healthy"})
		if err != nil {
			t.Fatal(err)
		}
		if err := opened.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(store.TranscriptPath("bad"), []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		listed, err := store.List()
		if err != nil {
			t.Errorf("one bad transcript hides healthy sessions: %v", err)
			continue
		}
		if len(listed) != 1 || listed[0].ID != "healthy" {
			t.Errorf("healthy session missing: %+v", listed)
		}
	}
}
