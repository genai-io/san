package llm

import (
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"

	"context"
	"errors"
	"testing"

	"github.com/genai-io/san/internal/core"
)

// --- mock provider for LLM tests ---

// mockLLMProvider serves a queued sequence of answers, one per call, and says
// so once the queue is dry. What a provider owns is reaching the endpoint; the
// model behind it is the SDK's test double.
type mockLLMProvider struct {
	responses []CompletionResponse
	models    []ModelInfo
	listErr   error
	listCalls int

	driver *aitest.Driver
}

func (m *mockLLMProvider) Client(string, map[string]string) (*ai.Client, error) {
	if m.driver == nil {
		turns := make([]aitest.Turn, 0, len(m.responses))
		for _, r := range m.responses {
			turns = append(turns, aitest.Replies(ai.Response{
				Content:    r.Content,
				StopReason: r.StopReason,
				Usage:      ai.Usage{Input: r.Usage.Input, Output: r.Usage.Output},
			}))
		}
		m.driver = aitest.New(turns...)
	}
	return m.driver.Client(), nil
}

// lastSystem is the system prompt that reached the endpoint.
func (m *mockLLMProvider) lastSystem() string {
	if m.driver == nil || m.driver.Last() == nil {
		return ""
	}
	return m.driver.Last().System
}

func (m *mockLLMProvider) ListModels(_ context.Context) ([]ModelInfo, error) {
	m.listCalls++
	return m.models, m.listErr
}

func (m *mockLLMProvider) Name() string { return "mock" }

type mockLimitFetcherProvider struct {
	mockLLMProvider
	inputLimit  int
	outputLimit int
	fetchErr    error
}

func (m *mockLimitFetcherProvider) FetchModelLimits(_ context.Context, _ string) (int, int, error) {
	return m.inputLimit, m.outputLimit, m.fetchErr
}

// --- LLM tests ---

func TestCompleteCollectsTheStream(t *testing.T) {
	mp := &mockLLMProvider{
		responses: []CompletionResponse{
			{Content: ai.TextContent("hello"), StopReason: ai.StopEndTurn, Usage: Usage{Input: 10, Output: 5}},
		},
	}
	l := &Client{provider: mp, model: "test-model", maxTokens: 4096}

	msgs := []core.Message{{Role: ai.RoleUser, Content: ai.TextContent("hi")}}
	resp, err := Complete(context.Background(), mp, l.completionOpts(msgs, nil, "system prompt"))
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}
	if resp.Content.Text() != "hello" {
		t.Errorf("expected 'hello', got '%s'", resp.Content.Text())
	}
}

func TestLLMComplete(t *testing.T) {
	mp := &mockLLMProvider{
		responses: []CompletionResponse{
			{Content: ai.TextContent("summary"), StopReason: ai.StopEndTurn},
		},
	}
	l := &Client{provider: mp, model: "test-model"}

	resp, err := l.Complete(context.Background(), "compact", []core.Message{{Role: ai.RoleUser, Content: ai.TextContent("summarize")}}, 2048)
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}
	if resp.Content.Text() != "summary" {
		t.Errorf("expected 'summary', got '%s'", resp.Content.Text())
	}
}

func TestLLMNameAndModelID(t *testing.T) {
	l := &Client{provider: &mockLLMProvider{}, model: "claude-3"}
	if l.Name() != "mock" {
		t.Errorf("expected 'mock', got '%s'", l.Name())
	}
	if l.ModelID() != "claude-3" {
		t.Errorf("expected 'claude-3', got '%s'", l.ModelID())
	}
}

func TestResolveMaxTokens_CustomOverride(t *testing.T) {
	l := &Client{provider: &mockLLMProvider{}, model: "m", maxTokens: 16384}
	got := l.effectiveMaxTokens()
	if got != 16384 {
		t.Errorf("expected 16384, got %d", got)
	}
}

func TestResolveMaxTokens_FromProvider(t *testing.T) {
	mp := &mockLLMProvider{
		models: []ModelInfo{
			{ID: "claude-opus", MaxOutput: 32000},
			{ID: "claude-sonnet", MaxOutput: 24000},
		},
	}
	l := &Client{provider: mp, model: "claude-sonnet"} // maxTokens = 0

	got := l.effectiveMaxTokens()
	if got != 24000 {
		t.Errorf("expected 24000, got %d", got)
	}
}

func TestResolveMaxTokens_Fallback(t *testing.T) {
	mp := &mockLLMProvider{
		models: []ModelInfo{
			{ID: "other-model", MaxOutput: 32000},
		},
	}
	l := &Client{provider: mp, model: "unknown-model"} // no match

	got := l.effectiveMaxTokens()
	if got != defaultMaxTokens {
		t.Errorf("expected default %d, got %d", defaultMaxTokens, got)
	}
}

// TestModelLimitsMemoized guards the hot-path fix: the input and output limits
// resolve from ListModels (a live network call for OpenAI-family providers), so
// repeated queries — one per inference step — must reuse a cached result rather
// than call the provider each time.
func TestModelLimitsMemoized(t *testing.T) {
	mp := &mockLLMProvider{
		models: []ModelInfo{{ID: "m", ContextWindow: 200000, MaxOutput: 8000}},
	}
	l := &Client{provider: mp, model: "m"}

	for i := range 5 {
		if got := l.ContextWindow(); got != 200000 {
			t.Fatalf("ContextWindow call %d = %d, want 200000", i, got)
		}
		if got := l.effectiveMaxTokens(); got != 8000 {
			t.Fatalf("ResolveMaxTokens call %d = %d, want 8000", i, got)
		}
	}
	// One listing states both limits, so one call answers everything —
	// regardless of how many times either is queried.
	if mp.listCalls != 1 {
		t.Errorf("ListModels called %d times across 5 rounds, want 1 (memoized)", mp.listCalls)
	}
}

// TestModelLimitsRetryAfterFailure ensures a transient resolution failure is not
// cached as 0: the next query retries, and only a successful lookup is memoized.
func TestModelLimitsRetryAfterFailure(t *testing.T) {
	mp := &mockLLMProvider{listErr: errors.New("network down")}
	l := &Client{provider: mp, model: "m"}

	if got := l.ContextWindow(); got != 0 {
		t.Fatalf("ContextWindow during outage = %d, want 0", got)
	}
	if got := l.ContextWindow(); got != 0 {
		t.Fatalf("ContextWindow during outage (2nd) = %d, want 0", got)
	}
	if mp.listCalls != 2 {
		t.Errorf("ListModels called %d times during outage, want 2 (failures retry)", mp.listCalls)
	}

	// Provider recovers: the success is now cached and later calls stop hitting it.
	mp.listErr = nil
	mp.models = []ModelInfo{{ID: "m", ContextWindow: 200000}}
	if got := l.ContextWindow(); got != 200000 {
		t.Fatalf("ContextWindow after recovery = %d, want 200000", got)
	}
	afterRecovery := mp.listCalls
	if got := l.ContextWindow(); got != 200000 {
		t.Fatalf("ContextWindow cached = %d, want 200000", got)
	}
	if mp.listCalls != afterRecovery {
		t.Errorf("ListModels called again after success (%d→%d), want cached", afterRecovery, mp.listCalls)
	}
}

// An undiscoverable window resolves to 0 rather than a guess: proactive
// compaction then stays out of the way and the prompt-too-long retry recovers.
// Acting on an invented number would silently compact a conversation that had
// room, or never fire on one that did not.
func TestContextWindowUnknownStaysZero(t *testing.T) {
	l := &Client{provider: &mockLLMProvider{models: []ModelInfo{{ID: "m"}}}, model: "m"}

	if got := l.ContextWindow(); got != 0 {
		t.Fatalf("ContextWindow() = %d, want 0 for an unknown window", got)
	}
}

func TestResolveMaxTokens_FromModelLimitsFetcher(t *testing.T) {
	mp := &mockLimitFetcherProvider{
		mockLLMProvider: mockLLMProvider{
			models: []ModelInfo{{ID: "m"}},
		},
		outputLimit: 16000,
	}
	l := &Client{provider: mp, model: "m"}

	got := l.effectiveMaxTokens()
	if got != 16000 {
		t.Errorf("expected 16000, got %d", got)
	}
}

// A listing that states no window sends the resolver to the endpoint that
// answers per model — Model Studio publishes limits nowhere else.
func TestModelLimitsFallBackToTheFetcher(t *testing.T) {
	mp := &mockLimitFetcherProvider{
		mockLLMProvider: mockLLMProvider{models: []ModelInfo{{ID: "m"}}},
		inputLimit:      400000,
		outputLimit:     8192,
	}

	in, out, _ := resolveModelLimits(mp, "m")
	if in != 400000 || out != 8192 {
		t.Errorf("resolveModelLimits() = (%d, %d), want (400000, 8192)", in, out)
	}
}

func TestCompletionOptsDefaultMaxTokens(t *testing.T) {
	l := &Client{provider: &mockLLMProvider{}, model: "m"}
	opts := l.completionOpts(nil, nil, "")
	if opts.MaxTokens != defaultMaxTokens {
		t.Errorf("expected default %d, got %d", defaultMaxTokens, opts.MaxTokens)
	}
}

func TestCompletionOptsIncludesThinkingEffort(t *testing.T) {
	l := &Client{
		provider:       &mockLLMProvider{},
		model:          "m",
		thinkingEffort: "high",
	}
	opts := l.completionOpts(nil, nil, "system")
	if opts.ThinkingEffort != "high" {
		t.Fatalf("expected thinking effort high, got %q", opts.ThinkingEffort)
	}
	if opts.SystemPrompt != "system" {
		t.Fatalf("expected system prompt to be preserved, got %q", opts.SystemPrompt)
	}
}

// Unknown has to stay 0 rather than become a guess: a guessed window is acted
// on silently and is wrong in both directions.
func TestModelLimitsReportUnknownAsZero(t *testing.T) {
	if in, out, answered := resolveModelLimits(nil, "m"); in != 0 || out != 0 || answered {
		t.Errorf("no provider = (%d, %d, %v), want (0, 0, false)", in, out, answered)
	}
	if in, out, answered := resolveModelLimits(&mockLLMProvider{listErr: errors.New("boom")}, "m"); in != 0 || out != 0 || answered {
		t.Errorf("failed listing = (%d, %d, %v), want (0, 0, false)", in, out, answered)
	}
}

// A provider that answered "I don't know" has answered. Re-asking re-fetches an
// entire endpoint catalog, and ContextWindow runs inside the agent's step loop —
// so this was a network round-trip per step of every turn, for any model whose
// window nobody publishes.
func TestAnUnknownWindowIsStillAnAnswer(t *testing.T) {
	mp := &mockLLMProvider{models: []ModelInfo{{ID: "m"}}} // listed, no window stated
	l := &Client{provider: mp, model: "m"}

	for range 5 {
		if got := l.ContextWindow(); got != 0 {
			t.Fatalf("ContextWindow() = %d, want 0 (unknown)", got)
		}
		if got := l.effectiveMaxTokens(); got != defaultMaxTokens {
			t.Fatalf("effectiveMaxTokens() = %d, want the default", got)
		}
	}
	if mp.listCalls != 1 {
		t.Errorf("ListModels called %d times across 5 rounds, want 1", mp.listCalls)
	}
}

// One listing answers both questions; resolving them separately paid for the
// same round-trip twice.
func TestModelLimitsResolveBothFromOneListing(t *testing.T) {
	mp := &mockLLMProvider{models: []ModelInfo{{ID: "m", ContextWindow: 200000, MaxOutput: 8192}}}
	l := &Client{provider: mp, model: "m"}

	if got := l.ContextWindow(); got != 200000 {
		t.Fatalf("ContextWindow() = %d, want 200000", got)
	}
	if got := l.effectiveMaxTokens(); got != 8192 {
		t.Fatalf("effectiveMaxTokens() = %d, want 8192", got)
	}
	if mp.listCalls != 1 {
		t.Errorf("ListModels called %d times, want 1", mp.listCalls)
	}
}

// --- streaming failure paths ---

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
	driver *aitest.Driver
}

func (p *retryThenSuccessProvider) Client(string, map[string]string) (*ai.Client, error) {
	if p.driver == nil {
		// An overloaded endpoint is retryable, so the next attempt runs.
		p.driver = aitest.New(
			aitest.Fails(&ai.Error{Kind: ai.KindOverloaded, Message: "overloaded"}),
			aitest.Says("recovered"),
		)
	}
	return p.driver.Client(), nil
}

func (*retryThenSuccessProvider) ListModels(context.Context) ([]ModelInfo, error) { return nil, nil }
func (*retryThenSuccessProvider) Name() string                                    { return "retry-stream" }

func TestCompleteRetriesOpaqueStreamError(t *testing.T) {
	provider := &retryThenSuccessProvider{}
	client := NewClient(provider, "test-model", 1)

	resp, err := client.Complete(context.Background(), "", nil, 1)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if resp.Content.Text() != "recovered" {
		t.Fatalf("Complete() content = %q, want recovered", resp.Content.Text())
	}
	if provider.driver.Calls() != 2 {
		t.Fatalf("Stream() calls = %d, want 2", provider.driver.Calls())
	}
}

// --- the seam the agent loop reaches the endpoint through ---

// headerRecordingProvider is an endpoint whose headers depend on the turn, and
// which remembers what it was handed. Copilot is the real one.
type headerRecordingProvider struct {
	mockLLMProvider
	got map[string]string
}

func (p *headerRecordingProvider) TurnHeaders(msgs []core.Message) map[string]string {
	for _, msg := range msgs {
		if msg.Content.HasImages() {
			return map[string]string{"X-Initiator": "user", "Copilot-Vision-Request": "true"}
		}
	}
	return map[string]string{"X-Initiator": "user"}
}

func (p *headerRecordingProvider) Client(model string, headers map[string]string) (*ai.Client, error) {
	p.got = headers
	return p.mockLLMProvider.Client(model, headers)
}

// A turn's headers have to reach the endpoint that asked for them. Copilot
// rejects image content outright unless the request opts into vision, so a
// client built without them cannot send a picture at all.
func TestTurnClientCarriesTheTurnsOwnHeaders(t *testing.T) {
	p := &headerRecordingProvider{}
	client := NewClient(p, "mock", 0)

	if _, err := client.TurnClient([]core.Message{
		core.UserMessage("look", []core.Attachment{{Image: ai.Image{MediaType: "image/png", Data: "x"}}}),
	}); err != nil {
		t.Fatalf("TurnClient: %v", err)
	}
	if p.got["Copilot-Vision-Request"] != "true" {
		t.Fatalf("headers = %v, want the turn's vision opt-in", p.got)
	}

	if _, err := client.TurnClient([]core.Message{{Role: ai.RoleUser, Content: ai.TextContent("hi")}}); err != nil {
		t.Fatalf("TurnClient: %v", err)
	}
	if _, asked := p.got["Copilot-Vision-Request"]; asked {
		t.Fatalf("headers = %v, want no vision opt-in on a text-only turn", p.got)
	}
}

// A provider whose headers never vary gets none, rather than an empty map that
// would key its client cache differently from a nil one.
func TestTurnClientSendsNoHeadersForAPlainProvider(t *testing.T) {
	if got := TurnHeaders(&mockLLMProvider{}, []core.Message{{Role: ai.RoleUser, Content: ai.TextContent("hi")}}); got != nil {
		t.Fatalf("TurnHeaders = %v, want nil", got)
	}
}

// The rung a person picked has to be on every call, and it changes mid-session
// — so it is read when the call is made, not when the client was built.
func TestCallOptionsCarryTheCurrentThinkingEffort(t *testing.T) {
	client := NewClient(&mockLLMProvider{}, "mock", 4096)
	client.SetThinkingEffort("high")

	req := &ai.Request{}
	for _, opt := range client.CallOptions() {
		opt(req)
	}
	if req.Effort != ai.EffortHigh {
		t.Errorf("Effort = %q, want high", req.Effort)
	}
	if req.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096", req.MaxTokens)
	}

	client.SetThinkingEffort("low")
	req = &ai.Request{}
	for _, opt := range client.CallOptions() {
		opt(req)
	}
	if req.Effort != ai.EffortLow {
		t.Errorf("Effort after the change = %q, want low", req.Effort)
	}
}

// A request needs prompt + max_tokens within the window, so the prompt budget
// is the window less the reply cap — and the cap is held to maxOutputReserve so
// a rarely-used 128k of output room does not eat the prompt's.
func TestPromptBudgetLeavesRoomForTheReply(t *testing.T) {
	for _, tc := range []struct {
		name              string
		window, maxOutput int
		want              int
	}{
		{"small output fits whole", 200_000, 8_000, 192_000},
		{"large output held to the reserve", 200_000, 64_000, 168_000},
		{"1M window, 128k output", 1_050_000, 128_000, 1_018_000},
		{"unknown output takes the default", 200_000, 0, 200_000 - defaultMaxTokens},
		{"unknown window is unknown", 0, 64_000, 0},
		{"window smaller than the cap", 16_000, 64_000, 0},
	} {
		if got := PromptBudget(tc.window, tc.maxOutput); got != tc.want {
			t.Errorf("%s: PromptBudget(%d, %d) = %d, want %d", tc.name, tc.window, tc.maxOutput, got, tc.want)
		}
	}
}

// The client asks for no more than the reserve, whatever the model allows.
func TestEffectiveMaxTokensHeldToTheReserve(t *testing.T) {
	mp := &mockLLMProvider{models: []ModelInfo{{ID: "m", ContextWindow: 200_000, MaxOutput: 64_000}}}
	l := &Client{provider: mp, model: "m"}
	if got := l.effectiveMaxTokens(); got != maxOutputReserve {
		t.Fatalf("effectiveMaxTokens() = %d, want %d", got, maxOutputReserve)
	}
	if got := l.PromptBudget(); got != 200_000-maxOutputReserve {
		t.Fatalf("PromptBudget() = %d, want %d", got, 200_000-maxOutputReserve)
	}
}

// The #338 follow-up: at 90% of a 200k window the prompt could reach 180k while
// the request still asked for 64k of reply — 244k, over the window. Any prompt
// under the budget now leaves the reply its room.
func TestPromptUnderBudgetAlwaysFitsWithItsReply(t *testing.T) {
	for _, m := range []struct{ window, maxOutput int }{
		{200_000, 64_000}, {400_000, 128_000}, {1_050_000, 128_000}, {128_000, 4_096},
	} {
		budget := PromptBudget(m.window, m.maxOutput)
		if prompt := budget - 1; prompt+OutputCap(m.maxOutput) > m.window {
			t.Errorf("window %d, output %d: prompt %d + reply %d overflows", m.window, m.maxOutput, prompt, OutputCap(m.maxOutput))
		}
	}
}
