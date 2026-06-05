package mode

import (
	"context"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/tool"
)

func TestAskUserQuestionRejectsEmptyQuestions(t *testing.T) {
	ask := NewAskUserQuestionTool()
	_, err := ask.PrepareInteraction(context.Background(), map[string]any{
		"questions": []any{},
	}, "/repo")
	if err == nil {
		t.Fatal("expected empty questions to be rejected")
	}
	if !strings.Contains(err.Error(), "must have 1-8 questions") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAskUserQuestionPreparesSingleQuestionWithEightOptions(t *testing.T) {
	ask := NewAskUserQuestionTool()
	req, err := ask.PrepareInteraction(context.Background(), map[string]any{
		"question": "What version should I release?",
		"options":  []string{"v1.15.2", "v1.16.0", "v2.0.0", "patch", "minor", "major", "dry run", "cancel"},
	}, "/repo")
	if err != nil {
		t.Fatalf("PrepareInteraction() error: %v", err)
	}

	got := req.(*tool.QuestionRequest).Questions
	if len(got) != 1 {
		t.Fatalf("expected 1 question, got %d", len(got))
	}
	if len(got[0].Options) != 8 {
		t.Fatalf("expected 8 options, got %d", len(got[0].Options))
	}
}
