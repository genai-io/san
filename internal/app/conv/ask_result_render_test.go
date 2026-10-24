package conv

import (
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
)

func TestAskUserResultShowsAnswerWithoutRepeatingQuestion(t *testing.T) {
	input := `{"question":"你觉得效果如何？","options":["符合预期","需要调整"]}`
	call := core.ToolCall{ID: "ask-1", Name: tool.ToolAskUserQuestion, Input: input}
	out := stripANSI(RenderToolCalls(ToolCallsParams{
		ToolCalls: []core.ToolCall{call},
		ResultMap: map[string]ToolResultData{call.ID: {
			ToolName: tool.ToolAskUserQuestion,
			Content:  "User responses:\n你觉得效果如何？ → 需要调整",
		}},
		Width: 120,
	}))
	if !strings.Contains(out, "● 你觉得效果如何？") || strings.Contains(out, "AskUserQuestion(") {
		t.Fatalf("question should replace tool name in call: %q", out)
	}
	if !strings.Contains(out, "  ↳ 需要调整") || strings.Contains(out, "⎿  →") {
		t.Fatalf("answer should use one curved arrow: %q", out)
	}
	if strings.Count(out, "你觉得效果如何？") != 1 {
		t.Fatalf("question repeated in result: %q", out)
	}
}

func TestAskUserCallShowsQuestionsWithoutToolNameWhilePendingOrExpanded(t *testing.T) {
	call := core.ToolCall{ID: "ask-2", Name: tool.ToolAskUserQuestion, Input: `{"questions":[{"question":"First?","options":["a","b"]},{"question":"Second?","options":["a","b"]}]}`}
	for _, expanded := range []bool{false, true} {
		out := stripANSI(RenderToolCalls(ToolCallsParams{
			ToolCalls: []core.ToolCall{call}, ToolCallsExpanded: expanded, Width: 80,
		}))
		if !strings.Contains(out, "● Q1: First?") || !strings.Contains(out, "Q2: Second?") || strings.Contains(out, "AskUserQuestion") {
			t.Fatalf("expanded=%v: question call = %q", expanded, out)
		}
	}
}

func TestAskUserCancelledCallKeepsQuestionAndShowsCancellation(t *testing.T) {
	call := core.ToolCall{ID: "ask-3", Name: tool.ToolAskUserQuestion, Input: `{"question":"Continue?","options":["yes","no"]}`}
	out := stripANSI(RenderToolCalls(ToolCallsParams{
		ToolCalls: []core.ToolCall{call},
		ResultMap: map[string]ToolResultData{call.ID: {
			ToolName: tool.ToolAskUserQuestion,
			Content:  "User cancelled the question prompt without answering.",
		}}, Width: 80,
	}))
	if !strings.Contains(out, "● Continue?") || !strings.Contains(out, "↳ Cancelled") || strings.Contains(out, "AskUserQuestion") {
		t.Fatalf("cancelled question = %q", out)
	}
}

func TestAskUserResultKeepsAnswersWithArrowsForMultipleQuestions(t *testing.T) {
	input := `{"questions":[{"question":"A → B?","options":["yes","no"]},{"question":"Why?","options":["reason","other"]}]}`
	out := stripANSI(renderAskUserResultInline(ToolResultData{
		ToolInput: input,
		Content:   "User responses:\nA → B? → yes\nWhy? → custom → answer",
	}))
	if want := "  ↳ Q1: yes\n  ↳ Q2: custom → answer\n"; out != want {
		t.Fatalf("rendered result = %q, want %q", out, want)
	}
}
