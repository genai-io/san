package app

import (
	"fmt"
	"testing"

	"github.com/genai-io/san/internal/core"
)

func BenchmarkResumeHistory(b *testing.B) {
	for _, count := range []int{200, 1000} {
		b.Run(fmt.Sprintf("messages=%d", count), func(b *testing.B) {
			messages := make([]core.ChatMessage, count)
			for i := range messages {
				messages[i] = core.ChatMessage{ID: fmt.Sprint(i), Role: core.ChatAssistant, Content: fmt.Sprintf("## Result %d\n\nA **formatted** response with `code`.\n\n- first item\n- second item", i)}
			}
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				m := fixedComposerModel(120, 40)
				m.conv.Messages = append([]core.ChatMessage(nil), messages...)
				b.StartTimer()
				_ = m.commitAllMessages()
				drainScrollback(m)
			}
		})
	}
}

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
