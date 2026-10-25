package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestInferencePlacementImmediatelyAboveComposer(t *testing.T) {
	for _, width := range []int{100, 40, 32} {
		for _, height := range []int{8, 12} {
			for _, pending := range []bool{false, true} {
				t.Run(fmt.Sprintf("%dx%d/pending=%v", width, height, pending), func(t *testing.T) {
					m := modelWithInferenceProgress(height)
					m.env.Width = width
					m.conv.Stream.StartedAt = time.Now().Add(-17 * time.Second)
					tracker := ""
					if pending {
						tracker = "Tasks · background"
						m.userInput.Queue.Enqueue("queued-message", nil)
					}
					frame, cursor := m.renderNormalView(strings.Repeat("─", width-1), tracker)
					plain := ansi.Strip(frame)
					lines := strings.Split(plain, "\n")
					status := -1
					for i, line := range lines {
						if strings.Contains(line, "✦") {
							status = i
						}
					}
					if strings.Count(plain, "✦") != 1 || status < 0 || status+1 >= len(lines) || !strings.HasPrefix(lines[status+1], "─") {
						t.Fatalf("inference not directly above rule:\n%s", plain)
					}
					if strings.Count(plain, "On it") != 1 || !strings.Contains(lines[status], "✦ On it · ") || ansi.StringWidth(lines[status]) >= width {
						t.Fatalf("fixed inference copy missing or exceeds narrow width:\n%s", plain)
					}
					for _, label := range []string{"Tasks", "queued-message"} {
						if index := strings.Index(plain, label); index >= 0 && index > strings.Index(plain, "✦") {
							t.Fatalf("%s below inference:\n%s", label, plain)
						}
					}
					if pending && height == 12 && (!strings.Contains(plain, "Tasks") || !strings.Contains(plain, "queued-message")) {
						t.Fatalf("missing task/queue blocks:\n%s", plain)
					}
					if len(lines) != height || cursor == nil || cursor.Position.Y != height-3 {
						t.Fatalf("composer displaced: rows=%d cursor=%+v\n%s", len(lines), cursor, plain)
					}
				})
			}
		}
	}
}

func TestInferencePlacementModalSuppressesStandaloneRow(t *testing.T) {
	for _, width := range []int{100, 40, 32} {
		for _, height := range []int{8, 12} {
			for _, question := range []bool{false, true} {
				m := modelWithInferenceProgress(height)
				m.env.Width = width
				if question {
					m.conv.Modal.Question.Show(&tool.QuestionRequest{Questions: []tool.Question{{Question: "Continue?", Options: []tool.QuestionOption{{Label: "Yes"}}}}}, width)
				} else {
					m.userInput.Approval.Show(&perm.PermissionRequest{ID: "placement", ToolName: "Bash", Description: "check"}, width, height)
				}
				if frame := ansi.Strip(m.View().Content); strings.Contains(frame, "✦") {
					t.Fatalf("%dx%d question=%v duplicated inference under modal:\n%s", width, height, question, frame)
				}
			}
		}
	}
}

func TestInferencePlacementRemovedOnCompletion(t *testing.T) {
	m := modelWithInferenceProgress(12)
	conv.Update(m, &m.conv, conv.AgentOutboxMsg{Event: sdkagent.InferenceEnd{Attempt: 1, Response: &ai.Response{Content: ai.TextContent("done")}}})
	if frame := ansi.Strip(m.View().Content); strings.Contains(frame, "✦") {
		t.Fatalf("completed inference retained status:\n%s", frame)
	}
}
