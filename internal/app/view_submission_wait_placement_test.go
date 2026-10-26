package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestSubmissionWaitUsesDockedInferenceSlot(t *testing.T) {
	for _, width := range []int{100, 40, 32} {
		for _, height := range []int{8, 12, 24} {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				m := fixedComposerModel(width, height)
				m.conv.Stream.Active, m.conv.Stream.Waiting = true, true
				m.conv.Stream.StartedAt = time.Now().Add(-time.Second)
				m.userInput.Queue.Enqueue("next prompt", nil)
				view := m.View()
				plain := ansi.Strip(view.Content)
				lines := strings.Split(plain, "\n")
				if strings.Count(plain, "✦ On it") != 1 {
					t.Fatalf("initial wait missing or duplicated:\n%s", plain)
				}
				for i, line := range lines {
					if strings.Contains(line, "✦ On it") && (i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "─")) {
						t.Fatalf("initial wait was not directly above input:\n%s", plain)
					}
				}
				if len(lines) != height || view.Cursor == nil || view.Cursor.Position.Y != height-3 || m.conv.Stream.Inferring || len(m.conv.Messages) != 0 {
					t.Fatal("display-only submission wait moved input or invented inference state")
				}
			})
		}
	}
}
