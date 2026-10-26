package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestFooterHierarchySinglePendingOmitsGroupLabel(t *testing.T) {
	for _, width := range []int{100, 40, 32} {
		for _, height := range []int{8, 12, 24} {
			for _, selected := range []int{-1, 0} {
				t.Run(fmt.Sprintf("%dx%d/selected=%d", width, height, selected), func(t *testing.T) {
					m := modelWithInferenceProgress(height)
					m.env.Width = width
					m.userInput.Queue.Enqueue("next", nil)
					m.userInput.Queue.SelectIdx = selected
					separator := strings.Repeat("─", width-1)
					frame, cursor := m.renderNormalView(separator, "Tasks\n  ● active-worker")
					plain := ansi.Strip(frame)
					lines := strings.Split(plain, "\n")
					if len(lines) != height || cursor == nil || cursor.Position.Y != height-3 {
						t.Fatalf("composer displaced: rows=%d cursor=%+v\n%s", len(lines), cursor, plain)
					}
					if strings.Contains(plain, "Pending") || !strings.Contains(plain, "next") {
						t.Fatalf("single queued prompt must remain visible without a group label:\n%s", plain)
					}
					if selected < 0 && !strings.Contains(plain, "↑ edit") || selected >= 0 && strings.Contains(plain, "↑ edit") {
						t.Fatalf("queue edit hint does not match selection:\n%s", plain)
					}
					if strings.Count(plain, "✦ On it") != 1 || strings.Index(plain, "next") > strings.Index(plain, "✦ On it") {
						t.Fatalf("single pending prompt must precede inference:\n%s", plain)
					}
					for i, line := range lines {
						if strings.Contains(line, "next") && ansi.StringWidth(line) >= width {
							t.Fatalf("queue exceeds width: %q", line)
						}
						if strings.Contains(line, "✦ On it") && (i+1 >= len(lines) || lines[i+1] != separator) {
							t.Fatalf("inference lost input adjacency:\n%s", plain)
						}
					}
					footer, _ := m.renderFooter(separator)
					if strings.HasPrefix(ansi.Strip(footer), "\n\n") {
						t.Fatalf("removed caption left an empty grouping row: %q", ansi.Strip(footer))
					}
				})
			}
		}
	}
}

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
				if strings.Contains(plain, "Pending") || strings.Count(plain, "✦ On it") != 1 || !strings.Contains(plain, "editing") {
					t.Fatalf("lost actionable queue or duplicated groups:\n%s", plain)
				}
				at := -1
				for i, line := range lines {
					if strings.Contains(line, "✦ On it") {
						at = i
					}
					if strings.Contains(line, "more") || strings.Contains(line, "editing") {
						if ansi.StringWidth(line) >= width {
							t.Fatalf("queue exceeds width: %q", line)
						}
					}
				}
				if at < 1 || !strings.HasPrefix(lines[at+1], "─") || strings.Index(plain, "editing") > strings.Index(plain, "✦ On it") {
					t.Fatalf("incorrect dock order:\n%s", plain)
				}
				if height == 24 {
					if !strings.Contains(plain, "Tasks") || !strings.Contains(plain, "first-prompt") || !strings.Contains(plain, "second-prompt") || strings.Contains(plain, "more") || lines[at-1] != "" {
						t.Fatalf("roomy groups lack hierarchy:\n%s", plain)
					}
				} else {
					if strings.Contains(plain, "first-prompt") || !strings.Contains(plain, "+2 more") || strings.Contains(plain, "more above") || lines[at-1] == "" {
						t.Fatalf("tight layout spent rows on decoration:\n%s", plain)
					}
				}
			})
		}
	}
}
