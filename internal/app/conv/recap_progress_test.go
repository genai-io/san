package conv

import "testing"

func TestRenderRecapWithoutBodyKeepsOnlyMarkAndFacts(t *testing.T) {
	for _, tc := range []struct{ facts, want string }{
		{"", "※\n"},
		{"plan 1/2", "※\n  plan 1/2\n"},
	} {
		if got := stripANSI(RenderRecap("※", tc.facts, "", 40)); got != tc.want {
			t.Fatalf("an empty recap body must not add text or trailing spaces: got %q, want %q", got, tc.want)
		}
	}
}
