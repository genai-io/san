package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/genai-io/san/internal/core"
)

type streamErrorProvider struct {
	err error
}

func (p streamErrorProvider) Stream(context.Context, CompletionOptions) <-chan StreamChunk {
	ch := make(chan StreamChunk, 1)
	ch <- StreamChunk{Type: ChunkTypeError, Error: p.err}
	close(ch)
	return ch
}

func (streamErrorProvider) ListModels(context.Context) ([]ModelInfo, error) { return nil, nil }
func (streamErrorProvider) Name() string                                    { return "stream-error" }

type retryThenSuccessProvider struct {
	calls int
}

func (p *retryThenSuccessProvider) Stream(context.Context, CompletionOptions) <-chan StreamChunk {
	p.calls++
	ch := make(chan StreamChunk, 1)
	if p.calls == 1 {
		ch <- StreamChunk{Type: ChunkTypeError, Error: errors.New("opaque terminal stream error")}
	} else {
		ch <- StreamChunk{Type: ChunkTypeDone, Response: &CompletionResponse{Content: "recovered"}}
	}
	close(ch)
	return ch
}

func (*retryThenSuccessProvider) ListModels(context.Context) ([]ModelInfo, error) { return nil, nil }
func (*retryThenSuccessProvider) Name() string                                    { return "retry-stream" }

func TestInferWrapsOpaqueStreamErrorAsRetryable(t *testing.T) {
	original := errors.New("opaque terminal stream error")
	client := NewClient(streamErrorProvider{err: original}, "test-model", 1)

	chunks, err := client.Infer(context.Background(), core.InferRequest{})
	if err != nil {
		t.Fatalf("Infer() error = %v", err)
	}
	chunk, ok := <-chunks
	if !ok {
		t.Fatal("Infer() returned no error chunk")
	}
	var retryable core.RetryableError
	if !errors.As(chunk.Err, &retryable) {
		t.Fatalf("chunk error %v is not retryable", chunk.Err)
	}
	if !errors.Is(chunk.Err, original) {
		t.Fatal("chunk error does not preserve the provider error")
	}
}

func TestCompleteRetriesOpaqueStreamError(t *testing.T) {
	provider := &retryThenSuccessProvider{}
	client := NewClient(provider, "test-model", 1)

	resp, err := client.Complete(context.Background(), "", nil, 1)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if resp.Content != "recovered" {
		t.Fatalf("Complete() content = %q, want recovered", resp.Content)
	}
	if provider.calls != 2 {
		t.Fatalf("Stream() calls = %d, want 2", provider.calls)
	}
}
