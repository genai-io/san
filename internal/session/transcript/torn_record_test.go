package transcript

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// writeTwoTurns lays down a transcript with a few complete records.
func writeTwoTurns(t *testing.T, fs *FileStore, id string) {
	t.Helper()
	if err := fs.Start(context.Background(), StartCommand{
		SessionID: id, ProjectID: "proj", Provider: "anthropic", Model: "model", Time: time.Now(),
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, text := range []string{"first", "second"} {
		if err := fs.AppendMessage(context.Background(), AppendMessageCommand{
			SessionID: id, MessageID: id + "-" + text, Time: time.Now(),
			Role: "user", Content: []ContentBlock{{Type: "text", Text: text}},
		}); err != nil {
			t.Fatalf("AppendMessage(%s): %v", text, err)
		}
	}
}

// A crash mid-append leaves a partial final line — appendRecord uses O_APPEND
// and only fsyncs on turn boundaries. Rejecting the whole file for it meant one
// interrupted turn cost the user the entire session, even though every record
// before the tear was intact.
func TestLoadRecoversFromATornFinalRecord(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir, "proj")
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	writeTwoTurns(t, fs, "crashed")

	// Simulate the tear: the process died partway through writing a record.
	path := fs.TranscriptPath("crashed")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	if err := os.WriteFile(path, append(body, []byte(`{"id":"partial","sessi`)...), 0o644); err != nil {
		t.Fatalf("write torn transcript: %v", err)
	}

	reopened, err := NewFileStore(dir, "proj")
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	tr, err := reopened.Load(context.Background(), "crashed")
	if err != nil {
		t.Fatalf("Load rejected a session whose only damage is a torn tail: %v", err)
	}
	if len(tr.Messages) == 0 {
		t.Fatal("the intact records before the tear were lost")
	}
}

// Damage in the middle is not a crash signature. Dropping it silently would
// leave a hole in the replayed conversation with nothing to show for it, so it
// must still fail loudly.
func TestLoadStillRejectsDamageInTheMiddle(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir, "proj")
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	writeTwoTurns(t, fs, "corrupt")

	path := fs.TranscriptPath("corrupt")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 records, got %d", len(lines))
	}
	lines[1] = `{"id":"broken","typ` // damage a record that is not the last
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write corrupt transcript: %v", err)
	}

	reopened, err := NewFileStore(dir, "proj")
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if _, err := reopened.Load(context.Background(), "corrupt"); err == nil {
		t.Error("a damaged record in the middle was skipped silently; " +
			"the replayed conversation would have an invisible hole")
	}
}
