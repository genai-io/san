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
	if strings.Contains(plain, "Other") {
		t.Fatalf("the free-text row should say what to do, not show the Other label:\n%s", plain)
	}
	if !strings.Contains(plain, "4. Type custom response") || strings.Contains(plain, "5.") {
		t.Fatalf("the model's own Other option should be the one free-text row:\n%s", plain)
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

// What is typed sits on the free-text row itself, after its number, not on a
// line of its own below the options.
func TestQuestionPromptTypesOnTheOtherRow(t *testing.T) {
	p := NewQuestionPrompt()
	p.Show(&tool.QuestionRequest{
		ID: "ask-1",
		Questions: []tool.Question{{
			Question: "Which port?",
			Options:  []tool.QuestionOption{{Label: "8080"}, {Label: "3000"}},
		}},
	}, 80)
	p.handleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
	p.handleKeypress(tea.KeyPressMsg{Code: tea.KeyDown})
	for _, r := range "9090" {
		p.handleKeypress(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	plain := stripANSI(p.Render())
	var row string
	for line := range strings.SplitSeq(plain, "\n") {
		if strings.Contains(line, "3. ") {
			row = line
		}
	}
	if !strings.Contains(row, "3. 9090") {
		t.Fatalf("typed text should follow the row number, got row %q in:\n%s", row, plain)
	}
	if strings.Count(plain, "9090") != 1 {
		t.Fatalf("typed text should appear once, on its row:\n%s", plain)
	}
}
