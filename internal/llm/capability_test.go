package llm

import (
	"context"
	"testing"
)

// plainProvider implements Provider without declaring image support, so the
// helper must default it to true.
type plainProvider struct{}

func (plainProvider) Stream(context.Context, CompletionOptions) <-chan StreamChunk { return nil }
func (plainProvider) ListModels(context.Context) ([]ModelInfo, error)              { return nil, nil }
func (plainProvider) Name() string                                                 { return "plain" }

// textOnlyProvider opts out of image input via ImageSupportProvider.
type textOnlyProvider struct{ plainProvider }

func (textOnlyProvider) SupportsImages(string) bool { return false }

func TestSupportsImages(t *testing.T) {
	if !SupportsImages(plainProvider{}, "m") {
		t.Error("a provider that doesn't implement ImageSupportProvider should default to supported")
	}
	if SupportsImages(textOnlyProvider{}, "m") {
		t.Error("a provider that opts out should report no image support")
	}
}
