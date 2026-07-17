package openaicompat

import "testing"

func TestSplitInputTokens(t *testing.T) {
	tests := []struct {
		name       string
		fullInput  int
		cached     int
		wantFresh  int
		wantCached int
	}{
		{name: "no cache", fullInput: 1000, cached: 0, wantFresh: 1000, wantCached: 0},
		{name: "partial cache", fullInput: 1000, cached: 900, wantFresh: 100, wantCached: 900},
		{name: "fully cached", fullInput: 1000, cached: 1000, wantFresh: 0, wantCached: 1000},
		// Defensive normalization: malformed wire data must never yield a
		// negative fresh count or over-report the cached slice.
		{name: "cached exceeds input", fullInput: 500, cached: 900, wantFresh: 0, wantCached: 500},
		{name: "negative cached", fullInput: 500, cached: -10, wantFresh: 500, wantCached: 0},
		{name: "negative input", fullInput: -10, cached: 5, wantFresh: 0, wantCached: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fresh, cached := SplitInputTokens(tc.fullInput, tc.cached)
			if fresh != tc.wantFresh || cached != tc.wantCached {
				t.Fatalf("SplitInputTokens(%d, %d) = (%d, %d), want (%d, %d)",
					tc.fullInput, tc.cached, fresh, cached, tc.wantFresh, tc.wantCached)
			}
		})
	}
}
