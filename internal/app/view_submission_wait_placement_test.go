package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestMemoryPreparationSharesSubmissionWait(t *testing.T) {
	for _, tc := range []struct {
		name    string
		width   int
		elapsed time.Duration
		detail  string
	}{
		{"recall", 80, 3 * time.Second, "  (Memory)"},
		{"narrow recall", 32, 3 * time.Second, "  (Memory)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const height = 12
			m := fixedComposerModel(tc.width, height)
			m.memoryInput = &memoryInput{startedAt: time.Now().Add(-tc.elapsed)}
			m.userInput.Queue.Enqueue("next prompt", nil)
			view := m.View()
			plain := ansi.Strip(view.Content)
			lines := strings.Split(plain, "\n")
			if len(lines) != height || view.Cursor == nil || view.Cursor.Position.Y != height-3 {
				t.Fatalf("recall moved the composer:\n%s", plain)
			}
			status := lines[height-5]
			prefix := "✦ On it"
			if !strings.HasPrefix(status, prefix+" · ") || !strings.HasSuffix(status, tc.detail) || lines[height-4] != strings.Repeat("─", tc.width-1) ||
				strings.Count(plain, prefix) != 1 {
				t.Fatalf("expected one wait row above the composer:\n%s", plain)
			}
			if m.conv.Stream.Active || len(m.conv.Messages) != 0 {
				t.Fatal("recall progress must be display-only")
			}
			if tc.detail == "" && strings.Contains(status, "(") {
				t.Fatalf("narrow status should omit the detail: %q", status)
			}
			m.memoryInput.startedAt = time.Now()
			if out := ansi.Strip(m.View().Content); strings.Contains(out, "(Memory)") || !strings.Contains(out, prefix) {
				t.Fatalf("initial memory wait lost its status or flashed recall detail:\n%s", out)
			}
		})
	}
}

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
				if strings.Contains(plain, "✦ On it") || !strings.Contains(plain, "next prompt") || lines[height-4] != strings.Repeat("─", width-1) {
					t.Fatalf("queued prompt should replace the initial wait indicator:\n%s", plain)
				}
				if len(lines) != height || view.Cursor == nil || view.Cursor.Position.Y != height-3 || m.conv.Stream.Inferring || len(m.conv.Messages) != 0 {
					t.Fatal("display-only submission wait moved input or invented inference state")
				}
			})
		}
	}
}
