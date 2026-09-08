package testutil

import (
	"iter"

	"github.com/genai-io/san/internal/core"

	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"

	"context"
	"sync"

	"github.com/genai-io/san/internal/llm"
)

// FakeProvider is an llm.Provider that answers from a queue instead of a
// network. The suites hand one to llm.NewClient exactly the way the app hands
// it a real vendor, which is what makes a test of the loop a test of the loop
// and not of a stub in the middle.
//
// It implements llm.Provider and nothing more — Client, ListModels, Name. What
// a provider owns is reaching the endpoint; the model behind it is the SDK's
// own double. A fake that mirrors the contract it stands in for cannot drift
// from it; one that grows its own convenience methods starts testing itself.
//
//	fake := &testutil.FakeProvider{Responses: []llm.CompletionResponse{
//	    {Content: "hello", StopReason: "end_turn"},
//	}}
//	client := llm.NewClient(fake, "fake-model", 8192)
type FakeProvider struct {
	// Responses is the queue to answer from, consumed in order. Each call pops
	// the first entry; an exhausted queue answers "no more responses" rather
	// than blocking, so a test that under-primes it fails on its assertion
	// rather than on a timeout.
	Responses []llm.CompletionResponse

	// ProviderName is what Name reports. Defaults to "fake".
	ProviderName string

	// Calls records every request received, in order, for tests that assert on
	// what was actually sent.
	Calls []llm.CompletionOptions

	// ErrorAt injects ErrorValue on the Nth call (1-based); 0 disables it.
	// This is how a test drives the failure branch of a turn.
	ErrorAt    int
	ErrorValue error

	mu        sync.Mutex
	callCount int
	driver    *aitest.Driver
}

// Client hands over the SDK client this provider answers through.
func (f *FakeProvider) Client(string, map[string]string) (*ai.Client, error) {
	f.mu.Lock()
	if f.driver == nil {
		f.driver = aitest.Always(f.answer)
	}
	driver := f.driver
	f.mu.Unlock()
	return driver.Client(), nil
}

// answer is one call: record what was asked, then take the head of the queue.
// It is an aitest.Turn, which is handed the request — Sent would say the same
// afterwards, but Calls is what the suites read and it is read in San's shape.
func (f *FakeProvider) answer(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
	f.mu.Lock()
	msgs := make([]core.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, core.Message{Role: m.Role, Content: m.Content})
	}
	f.Calls = append(f.Calls, llm.CompletionOptions{SystemPrompt: req.System, Messages: msgs})
	fail := f.injectError()
	errValue := f.ErrorValue
	// An injected failure leaves the queue where it was: the response it would
	// have answered with is still owed to the next call.
	var resp llm.CompletionResponse
	if !fail {
		resp = f.next()
	}
	f.mu.Unlock()

	if fail {
		return aitest.Fails(errValue)(ctx, req)
	}
	return aitest.Replies(resp)(ctx, req)
}

// ListModels reports nothing: a queue serves whatever model it is asked for.
func (f *FakeProvider) ListModels(context.Context) ([]llm.ModelInfo, error) { return nil, nil }

// Name returns the provider name.
func (f *FakeProvider) Name() string {
	if f.ProviderName != "" {
		return f.ProviderName
	}
	return "fake"
}

// injectError reports whether this call is the one to fail. Callers hold f.mu.
func (f *FakeProvider) injectError() bool {
	f.callCount++
	return f.ErrorAt > 0 && f.callCount == f.ErrorAt
}

// next pops the head of the queue. Callers hold f.mu.
func (f *FakeProvider) next() llm.CompletionResponse {
	if len(f.Responses) == 0 {
		return llm.CompletionResponse{Content: ai.TextContent("no more responses"), StopReason: ai.StopEndTurn}
	}
	resp := f.Responses[0]
	f.Responses = f.Responses[1:]
	return resp
}

var _ llm.Provider = (*FakeProvider)(nil)
