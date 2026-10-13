package transcript

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai"
)

// writeTwoTurns lays down a transcript with a few complete records.
func writeTwoTurns(t *testing.T, fs *Store, id string) {
	t.Helper()
	if err := fs.Start(context.Background(), StartCommand{
		SessionID: id, Provider: "anthropic", Model: "model", Time: time.Now(),
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, text := range []string{"first", "second"} {
		if err := fs.Append(context.Background(), Record{SessionID: id, Time: time.Now(), Type: MessageAppended, Message: &ai.Message{ID: id + "-" + text, Role: "user", Content: ai.Content{ai.TextBlock(text)}}}); err != nil {
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
	fs, err := NewStore(dir, "proj")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
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

	reopened, err := NewStore(dir, "proj")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	tr, err := reopened.Load(context.Background(), "crashed")
	if err != nil {
		t.Fatalf("Load rejected a session whose only damage is a torn tail: %v", err)
	}
	if len(tr.Messages) == 0 {
		t.Fatal("the intact records before the tear were lost")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(append(body, []byte(`{"id":"partial","sessi`)...)) {
		t.Fatal("read-only Load modified the transcript")
	}
	parent := "crashed-second"
	for _, id := range []string{"continued-1", "continued-2"} {
		if err := reopened.Append(context.Background(), Record{SessionID: "crashed", ParentMessageID: parent, Time: time.Now(), Type: MessageAppended, Message: &ai.Message{ID: id, Role: ai.RoleUser, Content: ai.TextContent(id)}}); err != nil {
			t.Fatal(err)
		}
		parent = id
	}
	continued, err := reopened.Load(context.Background(), "crashed")
	if err != nil {
		t.Fatalf("continued transcript cannot be loaded: %v", err)
	}
	if len(continued.Messages) != 4 {
		t.Fatalf("continued chain has %d messages, want 4", len(continued.Messages))
	}
	if got, err := os.ReadFile(path); err != nil || !strings.HasPrefix(string(got), string(body)) {
		t.Fatal("tail repair changed intact earlier records")
	}
}

func TestAppendPreservesCompleteUnterminatedRecord(t *testing.T) {
	fs, err := NewStore(t.TempDir(), "proj")
	if err != nil {
		t.Fatal(err)
	}
	writeTwoTurns(t, fs, "no-newline")
	path := fs.TranscriptPath("no-newline")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimRight(string(body), "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(fs.baseDir, "proj")
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Append(context.Background(), Record{SessionID: "no-newline", Time: time.Now(), Type: NoticeAdded, Notice: &NoticeRecord{Text: "notice"}}); err != nil {
		t.Fatal(err)
	}
	records, err := reopened.loadRecordsLocked(path)
	if err != nil || len(records) != 4 {
		t.Fatalf("records = %d, err = %v", len(records), err)
	}
}

// Damage in the middle is not a crash signature. Dropping it silently would
// leave a hole in the replayed conversation with nothing to show for it, so it
// must still fail loudly.
func TestLoadStillRejectsDamageInTheMiddle(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewStore(dir, "proj")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
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

	reopened, err := NewStore(dir, "proj")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := reopened.Load(context.Background(), "corrupt"); err == nil {
		t.Error("a damaged record in the middle was skipped silently; " +
			"the replayed conversation would have an invisible hole")
	}
	if err := reopened.Append(context.Background(), Record{SessionID: "corrupt", Time: time.Now(), Type: NoticeAdded, Notice: &NoticeRecord{Text: "notice"}}); err == nil {
		t.Fatal("append accepted damage in the middle")
	}
}
