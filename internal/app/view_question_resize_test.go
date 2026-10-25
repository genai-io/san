package app

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/tool"
)

func TestQuestionResizesWithoutClippingTextOrHints(t *testing.T) {
	const questionText = "FIFO question before the security approval?"
	for _, width := range []int{32, 40} {
		m := interactionModel()
		question := interactionQuestion(m, context.Background(), "question")
		question.Question.Request.Questions = []tool.Question{{Header: "Choose", Question: questionText,
			Options: []tool.QuestionOption{{Label: "Continue review"}, {Label: "Skip review"}}}}
		m.enqueueInteraction(question)
		m.enqueueInteraction(interactionApproval(m, context.Background(), "queued"))
		m.handleWindowResize(tea.WindowSizeMsg{Width: width, Height: 12})
		view := m.View().Content
		plain := ansi.Strip(view)
		if strings.Count(plain, "\n")+1 > 12 {
			t.Fatalf("width %d: question exceeds panel height:\n%s", width, plain)
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) >= width {
				t.Fatalf("width %d: question reaches wrap column: %q", width, ansi.Strip(line))
			}
		}
		if !strings.Contains(strings.Join(strings.Fields(plain), " "), questionText) {
			t.Fatalf("width %d: question text was clipped:\n%s", width, plain)
		}
		for _, text := range []string{"Continue review", "Skip review", "Enter", "Esc"} {
			if !strings.Contains(plain, text) {
				t.Fatalf("width %d: missing choice or key %q:\n%s", width, text, plain)
			}
		}
		if m.interactions.active != question {
			t.Fatal("resize changed the active request")
		}
	}
}
