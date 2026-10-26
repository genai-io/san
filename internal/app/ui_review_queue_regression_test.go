package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestUIReviewCompactQueueBudgets(t *testing.T) {
	for _, width := range []int{30, 32, 40} {
		for _, count := range []int{1, 2, 10, 50} {
			for _, selected := range []int{-1, count - 1} {
				t.Run(fmt.Sprintf("%d/%d/%d", width, count, selected), func(t *testing.T) {
					m := fixedComposerModel(width, 12)
					for range count {
						m.userInput.Queue.Enqueue("你好 selected message with long text", []core.Attachment{{Image: ai.Image{FileName: "image.png"}}})
					}
					m.userInput.Queue.SelectIdx = selected
					row := ansi.Strip(m.renderQueuePreview())
					if ansi.StringWidth(row) >= width {
						t.Fatalf("row width %d >= %d: %q", ansi.StringWidth(row), width, row)
					}
					if !strings.Contains(row, "你好") || count > 1 && !strings.Contains(row, fmt.Sprintf("Pending · %d", count)) || count == 1 && strings.Contains(row, "Pending") {
						t.Fatalf("lost content/count: %q", row)
					}
				})
			}
		}
	}
}

func TestUIReviewCompactQueueHintMatchesActualRouting(t *testing.T) {
	m := fixedComposerModel(40, 12)
	m.userInput.Queue.Enqueue("first", nil)
	m.userInput.Queue.Enqueue("second", nil)
	preview := ansi.Strip(m.renderQueuePreview())
	if !strings.Contains(preview, "first") || strings.Contains(preview, "↑ edit") {
		t.Fatalf("misleading next-item preview: %q", preview)
	}
	m.userInput.EnterQueueSelection()
	if m.userInput.Textarea.Value() != "second" {
		t.Fatalf("Up target = %q", m.userInput.Textarea.Value())
	}
	first, ok := m.userInput.Queue.Dequeue()
	if !ok || first.Content != "first" {
		t.Fatalf("FIFO changed: %+v", first)
	}
}
