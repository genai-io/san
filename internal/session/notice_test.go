package session

import (
	"slices"
	"testing"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session/transcript"
)

// A notice is shown to the person, never the model; it is kept so a resume
// shows it again where it was: before the first message, or after the one it
// followed.
func TestNoticesComeBackWhereTheyWereShown(t *testing.T) {
	store, err := NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rec := (&Setup{Store: store, SessionID: "s"}).NewRecorder("main", "p", "m", 1000)
	add := func(id string, role ai.Role, text string) {
		rec.OnAgentEvent(sdkagent.MessageAdded{Message: core.Message{ID: id, Role: role, Content: ai.TextContent(text)}})
	}
	rec.RecordNotice("Joined shop as @api", false)
	add("m1", ai.RoleUser, "hi")
	rec.RecordNotice("@web joined group shop", false)
	rec.RecordNotice("From @web: done", true)
	add("m2", ai.RoleAssistant, "hello")

	sess, err := store.Load("s")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range sess.Messages {
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
	if !sess.Messages[3].AgentNotice {
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
