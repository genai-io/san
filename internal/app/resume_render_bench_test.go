package app

import (
	"fmt"
	"testing"

	"github.com/genai-io/san/internal/core"
)

func BenchmarkResumeRender(b *testing.B) {
	messages := make([]core.ChatMessage, 200)
	for i := range messages {
		messages[i] = core.ChatMessage{ID: fmt.Sprint(i), Role: core.ChatAssistant, Content: "## Result\n\nA **formatted** response with `code`.\n\n- first item\n- second item"}
	}
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		m := fixedComposerModel(120, 40)
		m.conv.Messages = append([]core.ChatMessage(nil), messages...)
		b.StartTimer()
		_ = m.commitAllMessages()
	}
}
