package core

import (
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"

	"context"
	"errors"
	"testing"
	"time"
)

func TestBackoffDelayFloorWins(t *testing.T) {
	// frac=0 zeroes the exponential term, so the Retry-After floor stands.
	if got := backoffDelay(1, 5*time.Second, 0); got != 5*time.Second {
		t.Fatalf("backoffDelay floor = %v, want 5s", got)
	}
}

func TestBackoffDelayGrowsAndCaps(t *testing.T) {
	// frac=1 (upper edge of full jitter): attempt n ≈ base·2^(n-1), capped.
	a1 := backoffDelay(1, 0, 1)
	a2 := backoffDelay(2, 0, 1)
	if a1 != backoffBase {
		t.Fatalf("attempt1 = %v, want %v", a1, backoffBase)
	}
	if a2 != 2*backoffBase {
		t.Fatalf("attempt2 = %v, want %v", a2, 2*backoffBase)
	}
	if capped := backoffDelay(20, 0, 1); capped != backoffMax {
		t.Fatalf("attempt20 = %v, want cap %v", capped, backoffMax)
	}
}

func TestBackoffSleepHonorsCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Use a floor large enough that, without the cancel check, the call would
	// block well past the test. A canceled ctx must return immediately.
	if err := BackoffSleep(ctx, 1, 10*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("BackoffSleep on canceled ctx = %v, want context.Canceled", err)
	}
}

// failsThenAnswers is an endpoint that is down for n calls and then works.
func failsThenAnswers(err error, n int) *aitest.Driver {
	turns := make([]aitest.Turn, 0, n+1)
	for range n {
		turns = append(turns, aitest.Fails(err))
	}
	return aitest.New(append(turns, aitest.Says("ok"))...)
}

func newRetryAgent(t *testing.T, d ai.Driver, maxRetries int, timeout time.Duration) *agent {
	t.Helper()
	ag := NewAgent(Config{
		ID:                      "test",
		Client:                  testClient(d),
		System:                  NewSystem(),
		Tools:                   NewTools(),
		MaxTurnRetries:          maxRetries,
		StreamFirstChunkTimeout: timeout,
		StreamIdleTimeout:       timeout,
	})
	go func() {
		for range ag.Outbox() {
		}
	}()
	a := ag.(*agent)
	a.SetMessages([]Message{UserMessage("hi", nil)})
	return a
}

// stalled is what a driver reports when a stream goes quiet: the loop replays
// what ai.IsRetryable admits, and a network failure is one. The backoff below
// it is San's only because two retry loops that are not the agent's still
// need one.
var stalled = &ai.Error{Kind: ai.KindNetwork, Message: "stream stalled"}

func TestThinkActRetriesTransientStreamError(t *testing.T) {
	llm := failsThenAnswers(stalled, 2)
	a := newRetryAgent(t, llm, 2, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := a.ThinkAct(ctx)
	if err != nil {
		t.Fatalf("ThinkAct returned error after retries: %v", err)
	}
	if result.Content != "ok" {
		t.Fatalf("content = %q, want ok", result.Content)
	}
	if llm.Calls() != 3 {
		t.Fatalf("Infer calls = %d, want 3 (2 failures + 1 success)", llm.Calls())
	}
}

func TestThinkActSurfacesErrorAfterMaxRetries(t *testing.T) {
	llm := aitest.Always(aitest.Fails(stalled))
	a := newRetryAgent(t, llm, 2, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := a.ThinkAct(ctx); err == nil {
		t.Fatal("ThinkAct should surface the error after exhausting retries")
	}
	if llm.Calls() != 3 { // 1 initial + 2 retries
		t.Fatalf("Infer calls = %d, want 3", llm.Calls())
	}
}

func TestThinkActDoesNotRetryFatalError(t *testing.T) {
	// A 400 as the driver hands it over — typed, so ai.IsRetryable leaves it
	// fatal.
	llm := aitest.Always(aitest.Fails(&ai.Error{
		Kind: ai.KindInvalidRequest, Status: 400, Message: "bad request",
	}))
	a := newRetryAgent(t, llm, 3, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := a.ThinkAct(ctx); err == nil {
		t.Fatal("ThinkAct should surface a fatal error")
	}
	if llm.Calls() != 1 {
		t.Fatalf("Infer calls = %d, want 1 (no retry on fatal)", llm.Calls())
	}
}

// The loop's retry budget is spent on the failures the SDK typed as transient.
// Nothing between the driver and the loop tags them, so streamInfer classifies
// as the failure leaves the stream — without that, a provider's 429 read as
// fatal and the turn died on a blip it was built to ride out.
func TestThinkActRetriesAProviderRateLimit(t *testing.T) {
	llm := failsThenAnswers(&ai.Error{Kind: ai.KindRateLimit, Status: 429, Message: "slow down"}, 2)
	a := newRetryAgent(t, llm, 2, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := a.ThinkAct(ctx)
	if err != nil {
		t.Fatalf("ThinkAct returned error after retries: %v", err)
	}
	if result.Content != "ok" {
		t.Fatalf("content = %q, want ok", result.Content)
	}
	if llm.Calls() != 3 {
		t.Fatalf("Infer calls = %d, want 3 (2 rate limits + 1 success)", llm.Calls())
	}
}

// A terminal failure the SDK could not type is a transport failure, which is
// the one place the stream rule differs from the completed-call rule.
func TestThinkActRetriesAnOpaqueStreamFailure(t *testing.T) {
	llm := failsThenAnswers(&ai.Error{Kind: ai.KindNetwork, Message: "unexpected EOF"}, 1)
	a := newRetryAgent(t, llm, 2, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := a.ThinkAct(ctx); err != nil {
		t.Fatalf("ThinkAct returned error after retries: %v", err)
	}
	if llm.Calls() != 2 {
		t.Fatalf("Infer calls = %d, want 2 (1 failure + 1 success)", llm.Calls())
	}
}

// The reactive half: an overflow the provider reported has to reach
// isPromptTooLong, or the loop retries a prompt that cannot fit instead of
// shrinking it. Deliberately not also retryable — one compaction, then the
// call goes out again.
func TestThinkActCompactsOnProviderContextOverflow(t *testing.T) {
	llm := failsThenAnswers(&ai.Error{
		Kind: ai.KindContextExceeded, Status: 400, Message: "prompt is too long",
	}, 1)
	compacted := 0
	ag := NewAgent(Config{
		ID:     "test",
		Client: testClient(llm),
		System: NewSystem(),
		Tools:  NewTools(),
		CompactFunc: func(context.Context, []Message) (string, error) {
			compacted++
			return "the story so far", nil
		},
	}).(*agent)
	go func() {
		for range ag.Outbox() {
		}
	}()
	ag.SetMessages([]Message{
		UserMessage("one", nil), AssistantMessage("two", "", nil), UserMessage("three", nil),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := ag.ThinkAct(ctx); err != nil {
		t.Fatalf("ThinkAct returned error: %v", err)
	}
	if compacted != 1 {
		t.Fatalf("compactions = %d, want 1", compacted)
	}
	if llm.Calls() != 2 {
		t.Fatalf("Infer calls = %d, want 2 (overflow, compact, retry)", llm.Calls())
	}
}

func TestStreamInferIdleTimeoutRetries(t *testing.T) {
	llm := aitest.Always(aitest.Hangs())
	a := newRetryAgent(t, llm, 1, 40*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := a.ThinkAct(ctx); err == nil {
		t.Fatal("ThinkAct should fail once a stalled stream exhausts retries")
	}
	if llm.Calls() != 2 { // 1 initial + 1 retry, each stalls
		t.Fatalf("Infer calls = %d, want 2 (idle-timeout retry)", llm.Calls())
	}
}

// TestThinkActReturnsResultWhenRetriesAreExhausted pins the Result contract on
// the failure path. A turn that dies on a provider outage has still appended
// messages and burned tokens, and the caller persists a run from its Result —
// so returning only an error silently discards the work. subagent's
// finalizeResult is the concrete casualty: no Result means the transcript is
// never written to disk.
func TestThinkActReturnsResultWhenRetriesAreExhausted(t *testing.T) {
	llm := aitest.Always(aitest.Fails(stalled))
	a := newRetryAgent(t, llm, 2, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := a.ThinkAct(ctx)
	if err == nil {
		t.Fatal("ThinkAct should surface the error after exhausting retries")
	}
	if result == nil {
		t.Fatal("ThinkAct returned nil Result on a failed turn; the caller cannot persist the run")
	}
	if result.StopReason != StopError {
		t.Errorf("StopReason = %q, want %q", result.StopReason, StopError)
	}
	if result.StopDetail == "" {
		t.Error("StopDetail is empty; it should carry the underlying failure")
	}
	if len(result.Messages) == 0 {
		t.Error("Messages is empty; the conversation is what the caller persists")
	}
}
