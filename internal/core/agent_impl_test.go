package core

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// drainInbox is non-blocking by default, but when pendingInput signals a
// UI-queued message is coming it waits a brief window and picks up a message
// that arrives just after the boundary — one a non-blocking drain would miss.
func TestDrainInboxWaitsForPendingInput(t *testing.T) {
	ag := NewAgent(Config{ID: "t", LLM: newBlockingLLM(1), System: NewSystem(), Tools: NewTools()})
	a := ag.(*agent)
	go func() {
		for range ag.Outbox() { // drain so ingest's MessageEvent never blocks
		}
	}()

	// No pendingInput → empty inbox drains to nothing immediately.
	if n, err := a.drainInbox(context.Background()); err != nil || n != 0 {
		t.Fatalf("non-blocking drain of empty inbox = (%d, %v), want (0, nil)", n, err)
	}

	// pendingInput set → a message arriving just after is still ingested.
	pending := &atomic.Bool{}
	pending.Store(true)
	a.pendingInput = pending
	go func() {
		time.Sleep(15 * time.Millisecond)
		a.inbox <- UserMessage("steer", nil)
	}()
	if n, err := a.drainInbox(context.Background()); err != nil || n != 1 {
		t.Fatalf("waiting drain = (%d, %v), want (1, nil)", n, err)
	}

	// pendingInput cleared → back to non-blocking, empty drain returns at once.
	pending.Store(false)
	if n, err := a.drainInbox(context.Background()); err != nil || n != 0 {
		t.Fatalf("non-blocking drain after clear = (%d, %v), want (0, nil)", n, err)
	}
}

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

func TestEstimatePromptTokensUsesConversationGrowth(t *testing.T) {
	got := estimatePromptTokens(1000, 2000, 3000)
	if got != 1500 {
		t.Fatalf("estimatePromptTokens() = %d, want 1500", got)
	}
}

func TestEstimatePromptTokensNeverDropsBelowLastKnownPromptSize(t *testing.T) {
	got := estimatePromptTokens(1000, 3000, 2000)
	if got != 1000 {
		t.Fatalf("estimatePromptTokens() = %d, want 1000", got)
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
