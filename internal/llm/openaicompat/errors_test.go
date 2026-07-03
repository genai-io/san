package openaicompat

import (
	"errors"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
)

func TestNormalizeAPIErrorIncludesNonAuthAPIMessage(t *testing.T) {
	err := NormalizeAPIError("openai:subscription", &openai.Error{
		StatusCode: 400,
		Message:    "unsupported parameter: reasoning.summary",
	})

	if !strings.Contains(err.Error(), "unsupported parameter") {
		t.Fatalf("NormalizeAPIError() = %v, want server message", err)
	}
}

func TestNormalizeAPIErrorPreservesNonAPIError(t *testing.T) {
	original := errors.New("network down")
	if got := NormalizeAPIError("openai", original); got != original {
		t.Fatalf("NormalizeAPIError() = %v, want original error", got)
	}
}
