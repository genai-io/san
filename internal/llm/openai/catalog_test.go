package openai

import "testing"

func TestOpenAILimits(t *testing.T) {
	cases := []struct {
		model      string
		wantInput  int
		wantOutput int
	}{
		{"gpt-6", 400_000, 16_384},
		{"gpt-5.5", 400_000, 16_384},
		{"gpt-5.4-mini", 400_000, 16_384},
		{"gpt-5", 400_000, 16_384},
		{"o1", 200_000, 100_000},
		{"o3-mini", 200_000, 100_000},
		{"o4", 200_000, 100_000},
		{"codex-latest", 400_000, 16_384},
		{"gpt-4.1", 1_048_576, 16_384},
		{"gpt-4o", 128_000, 16_384},
		{"unknown-model", 0, 0},
	}
	for _, c := range cases {
		input, output := openAILimits(c.model)
		if input != c.wantInput || output != c.wantOutput {
			t.Errorf("openAILimits(%q) = (%d, %d), want (%d, %d)", c.model, input, output, c.wantInput, c.wantOutput)
		}
	}
}
