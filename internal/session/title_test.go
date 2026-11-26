package session

import (
	"strings"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestGeneratedTitleWordBoundary(t *testing.T) {
	for _, char := range []string{"a", "界"} {
		for _, n := range []int{12, 30, 31} {
			prefix := strings.Repeat(char, n)
			text := prefix + " " + strings.Repeat(char, 60)
			want := prefix + "..."
			if n <= 30 {
				want = prefix + " " + strings.Repeat(char, 59-n) + "..."
			}
			if got := GenerateTitle([]ai.Message{eventText("m1", ai.RoleUser, text)}); got != want {
				t.Errorf("%s prefix of %d: got %q, want %q", char, n, got, want)
			}
		}
	}
}
