package core

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Compaction must record the synthetic summary as a normal message.appended
// (so replay can resolve the ID the next inference references) and emit a
// CompactEvent whose SummaryMessageID equals that summary's ID (so replay truncates
// the summarized-away history at the summary).
func TestCompactRecordsSummaryAppendAndBoundary(t *testing.T) {
	var captured []Event
	ag := NewAgent(Config{
		ID:     "test",
		LLM:    newBlockingLLM(1),
		System: NewSystem(),
		Tools:  NewTools(),
		CompactFunc: func(_ context.Context, _ []Message) (string, error) {
			return "the summary", nil
		},
		OnEvent: func(e Event) { captured = append(captured, e) },
	})
	a := ag.(*agent)

	// Drain the outbox so the blocking CompactEvent emit doesn't stall.
	go func() {
		for range ag.Outbox() {
		}
	}()

	a.SetMessages([]Message{
		UserMessage("hi", nil),
		AssistantMessage("hello", "", nil),
		UserMessage("tell me more", nil),
	})

	if !a.compact(context.Background()) {
		t.Fatal("compact() returned false")
	}

	var summaryAppend *Message
	var info *CompactInfo
	for _, e := range captured {
		switch e.Type {
		case OnAppend:
			if m, ok := e.Data.(Message); ok {
				mm := m
				summaryAppend = &mm
			}
		case OnCompact:
			if ci, ok := e.CompactInfo(); ok {
				c := ci
				info = &c
			}
		}
	}

	if summaryAppend == nil {
		t.Fatal("compact did not emit OnAppend for the summary message")
	}
	if summaryAppend.ID == "" {
		t.Fatal("summary message must carry a stable ID")
	}
	if info == nil {
		t.Fatal("compact did not emit OnCompact")
	}
	if info.SummaryMessageID != summaryAppend.ID {
		t.Fatalf("SummaryMessageID %q must equal the appended summary ID %q", info.SummaryMessageID, summaryAppend.ID)
	}

	msgs := a.snapshot()
	if len(msgs) != 1 || msgs[0].ID != summaryAppend.ID {
		t.Fatalf("post-compact chain must be the single summary, got %d messages", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "the summary") {
		t.Fatalf("summary content missing from chain: %q", msgs[0].Content)
	}
}

// Auto-compaction must announce its start (with the pre-compaction message
// count) before the blocking summarization call, so the UI can show progress
// instead of appearing frozen, and that start must precede the OnCompact.
func TestCompactEmitsStartBeforeBoundary(t *testing.T) {
	var captured []Event
	ag := NewAgent(Config{
		ID:     "test",
		LLM:    newBlockingLLM(1),
		System: NewSystem(),
		Tools:  NewTools(),
		CompactFunc: func(_ context.Context, _ []Message) (string, error) {
			return "the summary", nil
		},
		OnEvent: func(e Event) { captured = append(captured, e) },
	})
	a := ag.(*agent)

	go func() {
		for range ag.Outbox() {
		}
	}()

	a.SetMessages([]Message{
		UserMessage("hi", nil),
		AssistantMessage("hello", "", nil),
		UserMessage("tell me more", nil),
	})

	if !a.compact(context.Background()) {
		t.Fatal("compact() returned false")
	}

	startIdx, compactIdx := -1, -1
	for i, e := range captured {
		switch e.Type {
		case OnCompactStart:
			cs, ok := e.CompactStart()
			if !ok {
				t.Fatalf("OnCompactStart carried %T, want CompactStart", e.Data)
			}
			if cs.Count != 3 {
				t.Fatalf("CompactStart.Count = %d, want 3", cs.Count)
			}
			startIdx = i
		case OnCompact:
			compactIdx = i
		}
	}

	if startIdx < 0 {
		t.Fatal("compact did not emit OnCompactStart")
	}
	if compactIdx < 0 {
		t.Fatal("compact did not emit OnCompact")
	}
	if startIdx > compactIdx {
		t.Fatalf("OnCompactStart (idx %d) must precede OnCompact (idx %d)", startIdx, compactIdx)
	}
}

// Regression for #338: the compaction check must test the full prompt, not the
// uncached delta InputTokens holds under prompt caching (see TotalInputTokens).
func TestPromptTokensCountCachedPrompt(t *testing.T) {
	resp := InferResponse{Usage: Usage{
		InputTokens:              1_200,
		CacheReadInputTokens:     170_000,
		CacheCreationInputTokens: 20_000,
	}}

	const limit = 200_000
	if NeedsCompaction(resp.InputTokens, limit) {
		t.Fatal("precondition: uncached delta alone must look far below the threshold")
	}
	if !NeedsCompaction(resp.TotalInputTokens(), limit) {
		t.Fatalf("NeedsCompaction(%d, %d) = false, want true", resp.TotalInputTokens(), limit)
	}
}

// The provider's own count wins whenever it exists — the text estimate is a
// fallback, never a correction.
func TestCurrentPromptTokensPrefersMeasuredCount(t *testing.T) {
	a := newAgentForPromptSizing(t)
	a.SetMessages([]Message{UserMessage(strings.Repeat("x", 400_000), nil)})
	a.promptTokens = 12_345

	if got := a.currentPromptTokens(); got != 12_345 {
		t.Fatalf("currentPromptTokens() = %d, want the measured 12345", got)
	}
}

// With no measured count the estimate stands in. It has to notice a rebuilt
// agent seeded with a long history (session resume, model switch, toolset
// drift), whose first prompt can already be over the threshold, without
// tipping over on an ordinary short conversation.
func TestCurrentPromptTokensEstimatesUnmeasuredChain(t *testing.T) {
	const limit = 200_000
	cases := []struct {
		name           string
		content        string
		wantCompaction bool
	}{
		{"seeded history", strings.Repeat("x", 800_000), true},
		{"fresh conversation", "fix the login bug", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newAgentForPromptSizing(t)
			a.SetMessages([]Message{UserMessage(c.content, nil)})

			got := a.currentPromptTokens()
			if got == 0 {
				t.Fatal("currentPromptTokens() = 0, want an estimate")
			}
			if NeedsCompaction(got, limit) != c.wantCompaction {
				t.Fatalf("NeedsCompaction(%d, %d) = %v, want %v",
					got, limit, !c.wantCompaction, c.wantCompaction)
			}
		})
	}
}

func newAgentForPromptSizing(t *testing.T) *agent {
	t.Helper()
	ag := NewAgent(Config{
		ID:     "test",
		LLM:    newBlockingLLM(1),
		System: NewSystem(),
		Tools:  NewTools(),
	})
	go func() {
		for range ag.Outbox() {
		}
	}()
	return ag.(*agent)
}

// Compaction collapses the chain to a single summary, so the measurement taken
// before it must not survive: it would still read "full" against the tiny new
// chain and compact again on every following step. Zero suppresses the check
// until the next inference reports a fresh figure.
func TestApplyCompactionClearsPromptTokens(t *testing.T) {
	a := newAgentForPromptSizing(t)
	a.SetMessages([]Message{
		UserMessage("first", nil),
		{Role: RoleAssistant, Content: "reply"},
		UserMessage("second", nil),
	})
	a.promptTokens = 195_000

	a.applyCompaction(context.Background(), "summary", 3, "manual")

	if a.promptTokens != 0 {
		t.Fatalf("promptTokens = %d, want 0 after compaction", a.promptTokens)
	}
}

// A SigCompact applies an in-place compaction (replacing the chain with the
// precomputed summary, recording the manual boundary) and must NOT start a
// turn — otherwise the lone summary would trigger a spurious inference.
func TestIngestSigCompactAppliesInPlaceWithoutStartingTurn(t *testing.T) {
	var captured []Event
	ag := NewAgent(Config{
		ID:      "test",
		LLM:     newBlockingLLM(1),
		System:  NewSystem(),
		Tools:   NewTools(),
		OnEvent: func(e Event) { captured = append(captured, e) },
	})
	a := ag.(*agent)
	go func() {
		for range ag.Outbox() {
		}
	}()

	a.SetMessages([]Message{
		UserMessage("hi", nil),
		AssistantMessage("hello", "", nil),
		UserMessage("more", nil),
	})

	if a.ingest(context.Background(), Message{Signal: SigCompact, Content: "the summary"}) {
		t.Fatal("SigCompact must not start a turn")
	}

	msgs := a.snapshot()
	if len(msgs) != 1 || !strings.Contains(msgs[0].Content, "the summary") {
		t.Fatalf("SigCompact should compact in place to the single summary, got %d messages", len(msgs))
	}

	var info *CompactInfo
	for _, e := range captured {
		if e.Type == OnCompact {
			if ci, ok := e.CompactInfo(); ok {
				c := ci
				info = &c
			}
		}
	}
	if info == nil {
		t.Fatal("SigCompact should emit a CompactEvent")
	}
	if info.Trigger != "manual" {
		t.Fatalf("manual compaction trigger = %q, want manual", info.Trigger)
	}
	if info.SummaryMessageID == "" || info.SummaryMessageID != msgs[0].ID {
		t.Fatalf("boundary %q must equal the summary message ID %q", info.SummaryMessageID, msgs[0].ID)
	}

	if !a.ingest(context.Background(), UserMessage("next", nil)) {
		t.Fatal("a normal user message must start a turn")
	}
}

// blockingLLM blocks Infer until the caller pushes a release signal. The
// release channel is buffered so the test can enqueue signals without
// racing the agent goroutine's read of the field.
type blockingLLM struct {
	release chan struct{}
}

func newBlockingLLM(capacity int) *blockingLLM {
	return &blockingLLM{release: make(chan struct{}, capacity)}
}

func (b *blockingLLM) InputLimit() int { return 0 }

func (b *blockingLLM) Infer(ctx context.Context, _ InferRequest) (<-chan Chunk, error) {
	ch := make(chan Chunk, 1)
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
			ch <- Chunk{Err: ctx.Err()}
		case <-b.release:
			ch <- Chunk{
				Done: true,
				Response: &InferResponse{
					Content:    "released",
					StopReason: StopEndTurn,
				},
			}
		}
	}()
	return ch, nil
}

func TestInterruptCurrentTurnReturnsToWaitInsteadOfEndingRun(t *testing.T) {
	llm := newBlockingLLM(4)
	ag := NewAgent(Config{
		ID:     "test",
		LLM:    llm,
		System: NewSystem(),
		Tools:  NewTools(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- ag.Run(ctx) }()

	// Drain outbox in the background so emit calls don't block.
	go func() {
		for range ag.Outbox() {
		}
	}()

	// Kick off the first turn, then interrupt while Infer is blocked.
	ag.Inbox() <- Message{Role: RoleUser, Content: "first"}
	// turn is stored at the top of each inner-loop iteration, right
	// before ThinkAct is called — wait until that pointer is published.
	waitFor(t, "agent turn to be stored", func() bool {
		return ag.(*agent).turn.Load() != nil
	})

	done := ag.InterruptCurrentTurn()

	// InterruptCurrentTurn's done channel should close once ThinkAct
	// has fully unwound — i.e. before any racing caller-side mutation
	// of agent state can collide with the agent goroutine.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("InterruptCurrentTurn done channel did not close")
	}

	// Resume by sending a second message and releasing the LLM. The
	// release channel is buffered so the test never races the agent's
	// read of it. Waiting on turn.Load() instead of sleeping proves the
	// second turn actually entered Infer.
	ag.Inbox() <- Message{Role: RoleUser, Content: "second"}
	waitFor(t, "second turn to enter Infer", func() bool {
		return ag.(*agent).turn.Load() != nil
	})
	llm.release <- struct{}{}

	// Wait for the second turn to drain fully before sending SigStop so
	// the test asserts the resume path actually executed, rather than
	// passing because SigStop preempted a never-started second turn.
	waitFor(t, "second turn to unwind", func() bool {
		return ag.(*agent).turn.Load() == nil
	})

	ag.Inbox() <- Message{Signal: SigStop}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run returned error on shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after SigStop")
	}
}

// TestInterruptBetweenTurnsIsLatched verifies that an interrupt fired
// in the window between turns (when the turn pointer is nil) is not
// silently dropped — the next iteration of Run's inner loop must see
// the latch and bail back to waitForInput instead of starting a fresh
// ThinkAct the user already asked not to run.
func TestInterruptBetweenTurnsIsLatched(t *testing.T) {
	llm := newBlockingLLM(4)
	ag := NewAgent(Config{
		ID:     "test",
		LLM:    llm,
		System: NewSystem(),
		Tools:  NewTools(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- ag.Run(ctx) }()
	go func() {
		for range ag.Outbox() {
		}
	}()

	// Set the latch BEFORE the agent ever starts a turn. The agent is
	// blocked in waitForInput, so turn pointer is nil and Swap returns
	// nil — the only thing that should keep the cancel alive is the
	// pendingInterrupt latch.
	done := ag.InterruptCurrentTurn()
	select {
	case <-done:
	default:
		t.Fatal("between-turn interrupt should return an already-closed done channel")
	}

	// Send a message. Inner loop should consume the latch and bail
	// back to waitForInput WITHOUT starting Infer — i.e. without
	// reading `release`.
	ag.Inbox() <- Message{Role: RoleUser, Content: "should be ignored"}

	// Give the agent time to either bail (correct) or wedge in Infer
	// (broken). If broken, release was never read and turn pointer is
	// non-nil.
	waitFor(t, "agent to consume latch and re-enter waitForInput", func() bool {
		return ag.(*agent).turn.Load() == nil && !ag.(*agent).interruptPending.Load()
	})

	// Clean shutdown.
	ag.Inbox() <- Message{Signal: SigStop}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run returned error on shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after SigStop")
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", what)
}

func TestCanExecuteToolBatchInParallelOnlyAllowsReadOnlyTools(t *testing.T) {
	tests := []struct {
		name  string
		tasks []agentToolTask
		want  bool
	}{
		{
			name: "all read only",
			tasks: []agentToolTask{
				{call: ToolCall{Name: "Read"}},
				{call: ToolCall{Name: "Grep"}},
				{call: ToolCall{Name: "Glob"}},
			},
			want: true,
		},
		{
			name: "edit serializes batch",
			tasks: []agentToolTask{
				{call: ToolCall{Name: "Read"}},
				{call: ToolCall{Name: "Edit"}},
			},
			want: false,
		},
		{
			name: "bash serializes batch",
			tasks: []agentToolTask{
				{call: ToolCall{Name: "Bash"}},
				{call: ToolCall{Name: "Read"}},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canExecuteToolBatchInParallel(tt.tasks); got != tt.want {
				t.Fatalf("canExecuteToolBatchInParallel() = %v, want %v", got, tt.want)
			}
		})
	}
}
