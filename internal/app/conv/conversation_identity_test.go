package conv

import (
	"testing"

	"github.com/genai-io/san/internal/core"
)

func TestAppendReturnsStoredMessageIdentity(t *testing.T) {
	conversation := NewConversation()
	appended := conversation.Append(core.ChatMessage{Role: core.ChatUser, Content: "same text"})

	if appended.ID == "" {
		t.Fatal("Append returned an empty message ID")
	}
	if got := conversation.Messages[0].ID; got != appended.ID {
		t.Fatalf("stored ID = %q, returned ID = %q", got, appended.ID)
	}
	provider, ok := appended.ToMessage()
	if !ok {
		t.Fatal("appended user message did not convert to a provider message")
	}
	if provider.ID != appended.ID {
		t.Fatalf("provider message ID = %q, want %q", provider.ID, appended.ID)
	}
}
