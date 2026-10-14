package input

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
)

func TestCompactUsesNativeRequestCount(t *testing.T) {
	for _, count := range []int{0, 1, core.MinMessagesToCompact} {
		conversation := &conv.ConversationModel{}
		conn := &llm.Conn{}
		conn.SetProvider(&connectFailProvider{})
		built := 0
		controller := SlashCommandController{env: SlashCommandEnv{
			LLM: conn, Conversation: conversation,
			SpinnerTickCmd: func() tea.Cmd { return nil },
			BuildCompactRequest: func(focus, trigger string) conv.CompactRequest {
				built++
				return conv.CompactRequest{Messages: make([]core.Message, count), SummaryFocus: focus, Trigger: trigger}
			},
		}}
		_, cmd, err := controller.handleCompactCommand(context.Background(), " focus ")
		if err != nil || built != 1 {
			t.Fatalf("build count=%d, error=%v", built, err)
		}
		if count < core.MinMessagesToCompact {
			if cmd != nil || conversation.Compact.Active {
				t.Fatal("compacted insufficient native history")
			}
		} else if cmd == nil || !conversation.Compact.Active || conversation.Compact.Count != count || conversation.Compact.SummaryFocus != "focus" {
			t.Fatalf("compact ignored native history: %+v", conversation.Compact)
		}
	}
}
