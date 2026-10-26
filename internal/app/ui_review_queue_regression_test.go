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
					if !strings.Contains(row, "你好") || strings.Contains(row, "Pending") || count > 1 && !strings.Contains(row, fmt.Sprintf("+%d more", count-1)) || count == 1 && strings.Contains(row, "more") {
						t.Fatalf("lost content/count: %q", row)
					}
				})
			}
		}
	}
}

func TestQueuePreviewCountsOnlyHiddenMessages(t *testing.T) {
	for _, height := range []int{12, 24} {
		for _, count := range []int{1, 2, 5, 6, 10} {
			for _, selected := range []int{-1, count - 1} {
				t.Run(fmt.Sprintf("height=%d/count=%d/selected=%d", height, count, selected), func(t *testing.T) {
					m := fixedComposerModel(32, height)
					for i := range count {
						m.userInput.Queue.Enqueue(fmt.Sprintf("prompt-%d", i), nil)
					}
					m.userInput.Queue.SelectIdx = selected
					view := ansi.Strip(m.renderQueuePreview())
					limit := 1
					if height == 24 {
						limit = 5
					}
					visible := min(count, limit)
					hidden := count - visible
					if strings.Contains(view, "Pending") || strings.Count(view, "❭") != visible {
						t.Fatalf("unexpected label or visible message count: %q", view)
					}
					if hidden == 0 && strings.Contains(view, "more") || hidden > 0 && !strings.Contains(view, fmt.Sprintf("+%d more", hidden)) {
						t.Fatalf("overflow must count only hidden messages (%d): %q", hidden, view)
					}
					rows := strings.Split(view, "\n")
					wantRows := visible
					if hidden > 0 && height == 24 {
						wantRows++
					}
					if len(rows) != wantRows {
						t.Fatalf("overflow consumed unexpected rows: got %d want %d: %q", len(rows), wantRows, view)
					}
					for _, row := range rows {
						if ansi.StringWidth(row) >= 32 {
							t.Fatalf("queue exceeds safe width: %q", row)
						}
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
