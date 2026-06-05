package conv

import (
	"strings"
	"testing"

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
