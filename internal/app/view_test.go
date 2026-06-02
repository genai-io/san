package app

import (
	"strings"
	"testing"
)

func TestTailLines(t *testing.T) {
	five := "L0\nL1\nL2\nL3\nL4"

	tests := []struct {
		name     string
		in       string
		maxLines int
		want     string
	}{
		{"non-positive returns input", five, 0, five},
		{"negative returns input", five, -3, five},
		{"fewer lines than max returns input", "a\nb", 5, "a\nb"},
		{"exact fit returns input", five, 5, five},
		{"truncates to last N (latest)", five, 2, "L3\nL4"},
		{"single line cap keeps latest", five, 1, "L4"},
		{"empty string", "", 3, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tailLines(tt.in, tt.maxLines)
			if got != tt.want {
				t.Fatalf("tailLines(%q, %d) = %q, want %q", tt.in, tt.maxLines, got, tt.want)
			}
			// The result must never exceed maxLines rows when capping applies.
			if tt.maxLines > 0 {
				if n := strings.Count(got, "\n") + 1; n > tt.maxLines && got != tt.in {
					t.Fatalf("tailLines(%q, %d) returned %d lines, exceeds cap", tt.in, tt.maxLines, n)
				}
			}
		})
	}
}
