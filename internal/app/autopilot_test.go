package app

import (
	"context"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
)

type autopilotStubProvider struct {
	content string
}

func (s *autopilotStubProvider) Stream(_ context.Context, _ llm.CompletionOptions) <-chan llm.StreamChunk {
	ch := make(chan llm.StreamChunk, 1)
	ch <- llm.StreamChunk{Type: llm.ChunkTypeDone, Response: &llm.CompletionResponse{Content: s.content}}
	close(ch)
	return ch
}

func (s *autopilotStubProvider) ListModels(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (s *autopilotStubProvider) Name() string                                        { return "stub" }

func TestAutopilotRecentTranscriptIncludesCompletionEvidence(t *testing.T) {
	messages := []core.ChatMessage{
		{Role: core.RoleUser, Content: core.FormatCompactSummary("created the package skeleton")},
		{Role: core.RoleAssistant, Content: "Run the tests."},
		{Role: core.RoleUser, Content: "ok", ToolResult: &core.ToolResult{
			ToolName: "Bash", Content: "ok  github.com/genai-io/san/internal/app", IsError: false,
		}},
	}

	got := autopilotRecentTranscript(messages, 3000)
	for _, want := range []string{"session summary:", "created the package skeleton", "tool Bash result:", "ok  github.com"} {
		if !strings.Contains(got, want) {
			t.Errorf("recent transcript missing %q:\n%s", want, got)
		}
	}
}

func TestAutopilotRecentTranscriptSkipsEmptyCompactSummary(t *testing.T) {
	messages := []core.ChatMessage{
		{Role: core.RoleUser, Content: core.FormatCompactSummary("")},
		{Role: core.RoleAssistant, Content: "Working on it."},
	}

	got := autopilotRecentTranscript(messages, 3000)
	if strings.Contains(got, "Previous context:") {
		t.Errorf("empty compact summary leaked the sentinel marker:\n%s", got)
	}
	if strings.Contains(got, "session summary:") {
		t.Errorf("empty compact summary should be dropped, not emitted as a row:\n%s", got)
	}
}

func TestAutopilotDecideContinueRejectsContradictoryStates(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{"continue", `{"continue":true,"done":false,"instruction":"Run the tests."}`, false},
		{"done", `{"continue":false,"done":true,"instruction":""}`, false},
		{"handback", `{"continue":false,"done":false,"instruction":""}`, false},
		{"continue and done", `{"continue":true,"done":true,"instruction":"Run tests."}`, true},
		{"continue without instruction", `{"continue":true,"done":false,"instruction":""}`, true},
		{"done with instruction", `{"continue":false,"done":true,"instruction":"Do more."}`, true},
		{"stop with instruction", `{"continue":false,"done":false,"instruction":"Do more."}`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := autopilotDecideContinue(context.Background(), &autopilotStubProvider{content: tt.content}, "model", "system", "mission", "evidence")
			if (err != nil) != tt.wantErr {
				t.Fatalf("autopilotDecideContinue() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
