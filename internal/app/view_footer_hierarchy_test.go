package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestFooterHierarchyPendingGroupAdaptsToHeight(t *testing.T) {
	for _, width := range []int{100, 40, 32} {
		for _, height := range []int{8, 12, 24} {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				m := modelWithInferenceProgress(height)
				m.env.Width = width
				m.conv.Stream.StartedAt = time.Now().Add(-17 * time.Second)
				for _, content := range []string{"first-prompt", "second-prompt", "editing-now"} {
					m.userInput.Queue.Enqueue(content, nil)
				}
				m.userInput.Queue.SelectIdx = 2
				frame, cursor := m.renderNormalView(strings.Repeat("─", width-1), "Tasks\n  ● active-worker")
				plain := ansi.Strip(frame)
				lines := strings.Split(plain, "\n")
				if len(lines) != height || cursor == nil || cursor.Position.Y != height-3 {
					t.Fatalf("composer displaced: rows=%d cursor=%+v\n%s", len(lines), cursor, plain)
				}
				if strings.Count(plain, "Pending") != 1 || strings.Count(plain, "✦ On it") != 1 || !strings.Contains(plain, "editing") {
					t.Fatalf("lost actionable queue or duplicated groups:\n%s", plain)
				}
				at := -1
				for i, line := range lines {
					if strings.Contains(line, "✦ On it") {
						at = i
					}
					if strings.Contains(line, "Pending") || strings.Contains(line, "editing") {
						if ansi.StringWidth(line) >= width {
							t.Fatalf("queue exceeds width: %q", line)
						}
					}
				}
				if at < 1 || !strings.HasPrefix(lines[at+1], "─") || strings.Index(plain, "Pending") > strings.Index(plain, "✦ On it") {
					t.Fatalf("incorrect dock order:\n%s", plain)
				}
				if height == 24 {
					if !strings.Contains(plain, "Tasks") || !strings.Contains(plain, "first-prompt") || !strings.Contains(plain, "second-prompt") || !strings.Contains(plain, "\n\nPending · 3\n") || lines[at-1] != "" {
						t.Fatalf("roomy groups lack hierarchy:\n%s", plain)
					}
				} else {
					if strings.Contains(plain, "first-prompt") || strings.Contains(plain, "more above") || lines[at-1] == "" {
						t.Fatalf("tight layout spent rows on decoration:\n%s", plain)
					}
				}
			})
		}
	}
}
