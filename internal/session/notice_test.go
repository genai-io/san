package session

import (
	"context"
	"slices"
	"testing"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/transcript"
)

// A notice is shown to the person, never the model; it is kept so a resume
// shows it again where it was: before the first message, or after the one it
// followed.
func TestNoticesComeBackWhereTheyWereShown(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rec := mustSession(t, store.transcriptStore, CreateOptions{ID: "s", AgentID: "main"})
	add := func(id string, role ai.Role, text string) {
		rec.RecordEvent(sdkagent.MessageAdded{Message: core.Message{ID: id, Role: role, Content: ai.TextContent(text)}})
	}
	_ = rec.Append(context.Background(), transcript.Record{Type: transcript.NoticeAdded, Notice: &transcript.NoticeRecord{Text: "Joined shop as @api", Agent: false}})
	add("m1", ai.RoleUser, "hi")
	_ = rec.Append(context.Background(), transcript.Record{Type: transcript.NoticeAdded, Notice: &transcript.NoticeRecord{Text: "@web joined group shop", Agent: false}})
	_ = rec.Append(context.Background(), transcript.Record{Type: transcript.NoticeAdded, Notice: &transcript.NoticeRecord{Text: "From @web: done", Agent: true}})
	add("m2", ai.RoleAssistant, "hello")

	sess, err := store.Read("s")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range sess.ChatRows() {
		got = append(got, string(m.Role)+":"+m.Content)
	}
	want := []string{
		"notice:Joined shop as @api",
		"user:hi",
		"notice:@web joined group shop",
		"notice:From @web: done",
		"assistant:hello",
	}
	if len(got) != len(want) {
		t.Fatalf("messages = %q\nwant %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("message %d = %q, want %q", i, got[i], want[i])
		}
	}
	if !sess.ChatRows()[3].AgentNotice {
		t.Error("an agent notice came back as a plain one")
	}
}

func TestWithNoticesPlacement(t *testing.T) {
	msgs := []core.ChatMessage{
		{ID: "a", Role: core.ChatAssistant},
		{ID: "r", Role: core.ChatUser, Content: "result 1"},
		{ID: "r", Role: core.ChatUser, Content: "result 2"}, // one parallel-call turn
	}
	got := withNotices(msgs, []transcript.NoticeRecord{
		{AfterMessageID: "r", Text: "after the turn"},
		{AfterMessageID: "compacted-away", Text: "gone"},
	})
	var texts []string
	for _, m := range got {
		texts = append(texts, m.Content)
	}
	want := []string{"", "result 1", "result 2", "after the turn"}
	if !slices.Equal(texts, want) {
		t.Errorf("got %q, want %q: a notice follows a turn's last row, and one whose message was compacted away goes with it", texts, want)
	}
}
