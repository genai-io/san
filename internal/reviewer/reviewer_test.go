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

// stubProvider returns a canned completion for testing Judge without a network call.
type stubProvider struct {
	content string
	err     error
}

func (s *stubProvider) Stream(_ context.Context, _ llm.CompletionOptions) <-chan llm.StreamChunk {
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

func Test_Judge(t *testing.T) {
	req := Request{ToolName: "Bash", Args: map[string]any{"command": "go test ./..."}, CWD: "/repo"}

	t.Run("allow", func(t *testing.T) {
		r := New(&stubProvider{content: `{"decision":"allow","reason":"runs tests"}`}, "model")
		v, err := r.Judge(context.Background(), req)
		if err != nil || !v.Allow {
			t.Fatalf("Judge() = %+v, err=%v; want Allow", v, err)
		}
	})

	t.Run("escalate", func(t *testing.T) {
		r := New(&stubProvider{content: `{"decision":"escalate","reason":"risky"}`}, "model")
		v, err := r.Judge(context.Background(), req)
		if err != nil || v.Allow {
			t.Fatalf("Judge() = %+v, err=%v; want escalate", v, err)
		}
	})

	t.Run("provider error fails closed", func(t *testing.T) {
		r := New(&stubProvider{err: errors.New("timeout")}, "model")
		if _, err := r.Judge(context.Background(), req); err == nil {
			t.Fatal("Judge() err = nil, want error so caller escalates")
		}
	})

	t.Run("garbage response errors", func(t *testing.T) {
		r := New(&stubProvider{content: "no verdict here"}, "model")
		if _, err := r.Judge(context.Background(), req); err == nil {
			t.Fatal("Judge() err = nil, want error")
		}
	})

	t.Run("nil provider errors", func(t *testing.T) {
		r := New(nil, "model")
		if _, err := r.Judge(context.Background(), req); err == nil {
			t.Fatal("Judge() err = nil, want error")
		}
	})
}
