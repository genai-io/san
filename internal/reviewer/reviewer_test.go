package reviewer

import (
	"context"
	"errors"
	"testing"

	"github.com/genai-io/san/internal/llm"
)

func Test_parseVerdict(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantAllow bool
		wantErr   bool
	}{
		{"clean allow", `{"decision":"allow","reason":"runs the test suite"}`, true, false},
		{"clean escalate", `{"decision":"escalate","reason":"deletes user data"}`, false, false},
		{"fenced json", "```json\n{\"decision\":\"allow\",\"reason\":\"local build\"}\n```", true, false},
		{"prose wrapped", "Here is my verdict:\n{\"decision\":\"escalate\",\"reason\":\"uploads a file\"}", false, false},
		{"uppercase decision", `{"decision":"ALLOW","reason":"x"}`, true, false},
		{"whitespace decision", `{"decision":" escalate ","reason":"x"}`, false, false},
		{"no json", "I think this looks fine to me.", false, true},
		{"unknown decision", `{"decision":"maybe","reason":"x"}`, false, true},
		{"malformed json", `{"decision":"allow", reason}`, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseVerdict(tt.content)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseVerdict(%q) err=%v, wantErr=%v", tt.content, err, tt.wantErr)
			}
			if err == nil && got.Allow != tt.wantAllow {
				t.Errorf("parseVerdict(%q).Allow = %v, want %v", tt.content, got.Allow, tt.wantAllow)
			}
		})
	}
}

// stubProvider returns a canned completion for testing Permission without a network call.
type stubProvider struct {
	content          string
	err              error
	lastSystemPrompt string
}

func (s *stubProvider) Stream(_ context.Context, opts llm.CompletionOptions) <-chan llm.StreamChunk {
	s.lastSystemPrompt = opts.SystemPrompt
	ch := make(chan llm.StreamChunk, 1)
	if s.err != nil {
		ch <- llm.StreamChunk{Type: llm.ChunkTypeError, Error: s.err}
	} else {
		ch <- llm.StreamChunk{Type: llm.ChunkTypeDone, Response: &llm.CompletionResponse{Content: s.content}}
	}
	close(ch)
	return ch
}

func (s *stubProvider) ListModels(_ context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (s *stubProvider) Name() string                                          { return "stub" }

func Test_Permission(t *testing.T) {
	req := Request{ToolName: "Bash", Args: map[string]any{"command": "go test ./..."}, CWD: "/repo"}

	t.Run("allow", func(t *testing.T) {
		r := New(&stubProvider{content: `{"decision":"allow","reason":"runs tests"}`}, "model")
		v, err := r.Permission(context.Background(), req)
		if err != nil || !v.Allow {
			t.Fatalf("Permission() = %+v, err=%v; want Allow", v, err)
		}
	})

	t.Run("escalate", func(t *testing.T) {
		r := New(&stubProvider{content: `{"decision":"escalate","reason":"risky"}`}, "model")
		v, err := r.Permission(context.Background(), req)
		if err != nil || v.Allow {
			t.Fatalf("Permission() = %+v, err=%v; want escalate", v, err)
		}
	})

	t.Run("provider error fails closed", func(t *testing.T) {
		r := New(&stubProvider{err: errors.New("timeout")}, "model")
		if _, err := r.Permission(context.Background(), req); err == nil {
			t.Fatal("Permission() err = nil, want error so caller escalates")
		}
	})

	t.Run("garbage response errors", func(t *testing.T) {
		r := New(&stubProvider{content: "no verdict here"}, "model")
		if _, err := r.Permission(context.Background(), req); err == nil {
			t.Fatal("Permission() err = nil, want error")
		}
	})

	t.Run("nil provider errors", func(t *testing.T) {
		r := New(nil, "model")
		if _, err := r.Permission(context.Background(), req); err == nil {
			t.Fatal("Permission() err = nil, want error")
		}
	})
}

func Test_parseBashPromptReply(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		wantAnswer bool
		wantInput  string
		wantErr    bool
	}{
		{"answer yes", `{"action":"answer","input":"y"}`, true, "y", false},
		{"answer word", `{"action":"answer","input":"yes"}`, true, "yes", false},
		{"skip", `{"action":"skip"}`, false, "", false},
		{"fenced answer", "```json\n{\"action\":\"answer\",\"input\":\"1\"}\n```", true, "1", false},
		{"unknown action", `{"action":"maybe"}`, false, "", true},
		{"no json", "sure, type y", false, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseBashPromptReply(tt.content)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseBashPromptReply(%q) err=%v, wantErr=%v", tt.content, err, tt.wantErr)
			}
			if err == nil && (got.Answer != tt.wantAnswer || got.Input != tt.wantInput) {
				t.Errorf("parseBashPromptReply(%q) = %+v, want answer=%v input=%q", tt.content, got, tt.wantAnswer, tt.wantInput)
			}
		})
	}
}

func Test_BashPrompt(t *testing.T) {
	t.Run("answer", func(t *testing.T) {
		r := New(&stubProvider{content: `{"action":"answer","input":"y"}`}, "model")
		got, err := r.BashPrompt(context.Background(), "apt-get install foo", "Continue? [Y/n]")
		if err != nil || !got.Answer || got.Input != "y" {
			t.Fatalf("BashPrompt() = %+v, err=%v; want answer y", got, err)
		}
	})
	t.Run("skip", func(t *testing.T) {
		r := New(&stubProvider{content: `{"action":"skip"}`}, "model")
		got, err := r.BashPrompt(context.Background(), "cmd", "Overwrite? [y/N]")
		if err != nil || got.Answer {
			t.Fatalf("BashPrompt() = %+v, err=%v; want skip", got, err)
		}
	})
	t.Run("provider error", func(t *testing.T) {
		r := New(&stubProvider{err: errors.New("boom")}, "model")
		if _, err := r.BashPrompt(context.Background(), "cmd", "prompt"); err == nil {
			t.Fatal("BashPrompt() err = nil, want error so caller skips")
		}
	})
}

func Test_SystemPromptOverride(t *testing.T) {
	s := &stubProvider{content: `{"decision":"allow","reason":"ok"}`}
	r := New(s, "model")
	req := Request{ToolName: "Bash", Args: map[string]any{"command": "date"}}

	// The built-in system prompt is used until overridden.
	_, _ = r.Permission(context.Background(), req)
	if s.lastSystemPrompt != defaultSystemPrompt {
		t.Errorf("Permission used %q, want the built-in system prompt", s.lastSystemPrompt)
	}

	// A custom system prompt replaces it.
	r.SetSystemPrompt("MY CUSTOM SYSTEM PROMPT")
	_, _ = r.Permission(context.Background(), req)
	if s.lastSystemPrompt != "MY CUSTOM SYSTEM PROMPT" {
		t.Errorf("Permission used %q, want the custom system prompt", s.lastSystemPrompt)
	}

	// BashPrompt shares the same customizable system prompt — only the per-call
	// task differs, and that rides in the user message.
	_, _ = r.BashPrompt(context.Background(), "apt-get install foo", "Continue? [Y/n]")
	if s.lastSystemPrompt != "MY CUSTOM SYSTEM PROMPT" {
		t.Errorf("BashPrompt used %q, want the shared custom system prompt", s.lastSystemPrompt)
	}

	// A blank override keeps the current prompt (unreadable config → built-in).
	r.SetSystemPrompt("   ")
	if r.systemPrompt != "MY CUSTOM SYSTEM PROMPT" {
		t.Errorf("blank override changed the prompt to %q", r.systemPrompt)
	}
}
