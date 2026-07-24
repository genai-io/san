package openaicompat

import (
	"errors"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
)

func TestNormalizeAPIErrorPreservesAPIErrorChain(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		apiErr   *openai.Error
		want     []string
	}{
		{
			name:     "authentication",
			provider: "openai:subscription",
			apiErr:   &openai.Error{StatusCode: 401, Message: "invalid key"},
			want:     []string{"OpenAI authentication failed: invalid key", "OPENAI_API_KEY", "reconnect"},
		},
		{
			name:     "generic API error",
			provider: "openai:subscription",
			apiErr:   &openai.Error{StatusCode: 400, Message: "unsupported parameter: reasoning.summary"},
			want:     []string{"unsupported parameter: reasoning.summary"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeAPIError(tc.provider, tc.apiErr)
			for _, want := range tc.want {
				if !strings.Contains(got.Error(), want) {
					t.Fatalf("NormalizeAPIError() = %q, want substring %q", got, want)
				}
			}
			var recovered *openai.Error
			if !errors.As(got, &recovered) {
				t.Fatal("normalized error does not retain *openai.Error in chain")
			}
			if recovered != tc.apiErr {
				t.Fatalf("recovered API error = %p, want %p", recovered, tc.apiErr)
			}
		})
	}
}

func TestNormalizeAPIErrorPreservesNonAPIError(t *testing.T) {
	original := errors.New("network down")
	if got := NormalizeAPIError("openai", original); got != original {
		t.Fatalf("NormalizeAPIError() = %v, want original error", got)
	}
}
