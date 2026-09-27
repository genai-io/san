package conv

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/tool"
)

func TestQuestionPromptRenderUsesSingleOuterSeparators(t *testing.T) {
	p := NewQuestionPrompt()
	p.Show(&tool.QuestionRequest{
		ID: "ask-1",
		Questions: []tool.Question{{
			Question: "What version should I release?",
			Header:   "Choose",
			Options: []tool.QuestionOption{
				{Label: "Patch version"},
				{Label: "Minor version"},
			},
		}},
	}, 80)

	plain := stripANSI(p.Render())

	if strings.HasPrefix(plain, "─") {
		t.Fatalf("question prompt should rely on the modal wrapper for the top separator:\n%s", plain)
	}
	if !strings.HasSuffix(plain, strings.Repeat("─", 78)) {
		t.Fatalf("question prompt should end with a bottom separator:\n%s", plain)
	}
}

func TestQuestionPromptRenderDoesNotDuplicateOtherOption(t *testing.T) {
	p := NewQuestionPrompt()
	p.Show(&tool.QuestionRequest{
		ID: "ask-1",
		Questions: []tool.Question{{
			Question: "What version should I release?",
			Header:   "Choose",
			Options: []tool.QuestionOption{
				{Label: "Patch version"},
				{Label: "Minor version"},
				{Label: "Major version"},
				{Label: "Other"},
			},
		}},
	}, 80)

	plain := stripANSI(p.Render())
	if count := strings.Count(plain, "Other"); count != 1 {
		t.Fatalf("expected one Other option, got %d:\n%s", count, plain)
	}
	if !strings.Contains(plain, "4. Other - Type custom response") {
		t.Fatalf("existing Other option should be used for custom input:\n%s", plain)
	}
}

// With the cursor on the free-text row, typing answers: the first key opens
// the input and lands in it, so there is no Enter before the text — and a
// digit there is text, not the shortcut for that option.
func TestQuestionPromptTypesStraightIntoTheOtherRow(t *testing.T) {
	p := NewQuestionPrompt()
	p.Show(&tool.QuestionRequest{
		ID: "ask-1",
		Questions: []tool.Question{{
			Question: "Which port?",
			Header:   "Port",
			Options:  []tool.QuestionOption{{Label: "8080"}, {Label: "3000"}},
		}},
	}, 80)
	type_ := func(s string) (resp *QuestionResponseMsg) {
		for _, r := range s {
			_, resp = p.handleKeypress(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
		return resp
	}

	p.handleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
	p.handleKeypress(tea.KeyPressMsg{Code: tea.KeyDown}) // onto the Other row
	if resp := type_("9090"); resp != nil {
		t.Fatalf("typing answered before Enter: %+v", resp)
	}
	_, resp := p.handleKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})
	if resp == nil || resp.Response == nil {
		t.Fatal("Enter did not submit the typed answer")
	}
	if got := resp.Response.Answers[0]; len(got) != 1 || got[0] != "9090" {
		t.Fatalf("answer = %q, want the typed 9090", got)
	}
}
