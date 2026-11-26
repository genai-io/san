package recap

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestInstructions(t *testing.T) {
	base := Instructions("", "")
	if strings.Contains(base, "already sees") || strings.Contains(base, "AutoPilot") {
		t.Fatalf("unexpected context in bare prompt: %s", base)
	}
	got := Instructions("approval needed", "ship the release")
	if !strings.HasSuffix(got, "don't repeat them: approval needed.\nThe overall goal is AutoPilot's mission: ship the release\n</system-reminder>") {
		t.Fatalf("missing recap context: %s", got)
	}
}

func TestGenerate(t *testing.T) {
	failure := errors.New("completion failed")
	for _, wantErr := range []error{nil, failure} {
		text, err := Generate(t.Context(), func(context.Context) (ai.Response, error) {
			return ai.Response{Content: ai.TextContent("  next action\n")}, wantErr
		})
		if text != "next action" || err != wantErr {
			t.Fatalf("Generate returned (%q, %v), want (%q, %v)", text, err, "next action", wantErr)
		}
	}
}
