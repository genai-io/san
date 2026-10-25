package conv

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/core"
)

func TestActiveWaitingFollowsNotices(t *testing.T) {
	for _, width := range []int{32, 40, 80} {
		for _, thinking := range []string{"", "Prior reasoning"} {
			for _, waiting := range []bool{false, true} {
				t.Run(fmt.Sprintf("width%d/reasoning%t/waiting%t", width, thinking != "", waiting), func(t *testing.T) {
					messages := []core.ChatMessage{
						{ID: "prior", Role: core.ChatAssistant, Content: "Settled answer"},
						{ID: "live", Role: core.ChatAssistant, Thinking: thinking},
						{Role: core.ChatNotice, AgentNotice: true, Content: "Background job succeeded"},
						{Role: core.ChatNotice, AgentNotice: true, Content: "Background job failed · exit status 1"},
					}
					before := append([]core.ChatMessage(nil), messages...)
					p := RenderContext{Messages: messages, Width: width, StreamActive: true, StreamWaiting: waiting, StreamMessageID: "live", StreamStartedAt: time.Now().Add(-49 * time.Second), ThinkingDisplay: "full"}
					out := ansi.Strip(RenderActiveContent(p))
					timer := strings.LastIndex(out, "49s")
					success := strings.Index(out, "succeeded")
					failure := strings.Index(out, "failed")
					if success < 0 || failure <= success || timer <= failure || strings.Count(out, "49s") != 1 {
						t.Fatalf("notice/wait order: %q", out)
					}
					if strings.Index(out, "Settled answer") >= success {
						t.Fatalf("settled answer moved: %q", out)
					}
					if thinking != "" && (strings.Index(out, thinking) < 0 || strings.Index(out, thinking) >= success) {
						t.Fatalf("reasoning moved: %q", out)
					}
					if !reflect.DeepEqual(messages, before) {
						t.Fatal("render mutated transcript")
					}
				})
			}
		}
	}
}

func TestActiveWaitingFollowsPersistedTaskEnvelope(t *testing.T) {
	for _, width := range []int{32, 40, 80} {
		p := RenderContext{Width: width, StreamActive: true, StreamMessageID: "live", StreamStartedAt: time.Now().Add(-49 * time.Second), Messages: []core.ChatMessage{
			{ID: "live", Role: core.ChatAssistant},
			{Role: core.ChatUser, Content: "<task-notification status=\"failed\" description=\"job\">\nError: exit status 1\n</task-notification>"},
		}}
		out := ansi.Strip(RenderActiveContent(p))
		if failure, timer := strings.Index(out, "failed"), strings.Index(out, "49s"); failure < 0 || timer <= failure || strings.Count(out, "49s") != 1 || strings.Contains(out, "<task-notification") {
			t.Fatalf("width %d: persisted notification did not precede the single waiting row: %q", width, out)
		}
	}
}

func TestActiveStreamingTextStaysBeforeNotice(t *testing.T) {
	for _, width := range []int{32, 40, 80} {
		p := RenderContext{Width: width, StreamActive: true, StreamMessageID: "live", StreamStartedAt: time.Now().Add(-49 * time.Second), Messages: []core.ChatMessage{
			{ID: "live", Role: core.ChatAssistant, Content: "Real streamed response"},
			{Role: core.ChatNotice, Content: "Background failed"},
		}}
		out := ansi.Strip(RenderActiveContent(p))
		if strings.Index(out, "Real streamed response") < 0 || strings.Index(out, "Real streamed response") >= strings.Index(out, "Background failed") || strings.Contains(out, "49s") {
			t.Fatalf("width %d: streamed text moved or waiting duplicated: %q", width, out)
		}
	}
}
