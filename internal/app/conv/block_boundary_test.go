package conv

import "testing"

func TestCompletedBlockBoundary(t *testing.T) {
	tests := []struct {
		name    string
		content string
		// want is the prefix the boundary should cover; the remainder after it
		// is the still-streaming block held back in the live view.
		want string
	}{
		{"empty", "", ""},
		{"single in-progress line", "hello world", ""},
		{"one paragraph, no trailing blank", "para one\nstill going", ""},
		{"one paragraph closed by blank line", "para one\n\nsecond", "para one\n\n"},
		{"soft-wrapped paragraph stays whole", "line a\nline b\n\nnext", "line a\nline b\n\n"},
		{"multiple closed paragraphs", "a\n\nb\n\nc", "a\n\nb\n\n"},
		{"trailing blank line", "a\n\n", "a\n\n"},
		{"open code fence holds everything", "```go\nx := 1\n", ""},
		{"blank line inside fence is not a boundary", "```go\n\nx := 1\n", ""},
		{"closed code fence commits without trailing blank", "```go\nx := 1\n```\nrest", "```go\nx := 1\n```\n"},
		{"text then closed fence", "intro\n\n```\ncode\n```\ntail", "intro\n\n```\ncode\n```\n"},
		{"table held until trailing blank", "| a | b |\n|---|---|\n| 1 | 2 |", ""},
		{"table held after blank line", "| a | b |\n|---|---|\n| 1 | 2 |\n\nafter", ""},
		{"paragraph before table can commit", "intro\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\nafter", "intro\n\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompletedBlockBoundary(tt.content)
			if got != len(tt.want) {
				t.Fatalf("boundary = %d (%q), want %d (%q)",
					got, tt.content[:got], len(tt.want), tt.want)
			}
			if tt.content[:got] != tt.want {
				t.Fatalf("committed prefix = %q, want %q", tt.content[:got], tt.want)
			}
		})
	}
}
