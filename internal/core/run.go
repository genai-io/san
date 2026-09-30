package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"

	glog "github.com/genai-io/san/internal/log"
)

// agent is the mailbox that waits for work and the exchange it hands each
// batch to. One file because they are one struct.
//
// The mailbox is the shape the SDK deliberately does not have: how messages
// are batched into exchanges is the application's to decide. Inside an
// exchange — the inference and its retries, the tool batch and its
// parallelism, compaction — is sdkagent.Agent's, and ThinkAct drives it.
//
// Two things an exchange asks for live elsewhere: what the model may call is
// toolset.go, and shortening the conversation is compact.go.
type agent struct {
	id            string
	system        System
	tools         *Tools
	compactFunc   func(ctx context.Context, msgs []Message) (string, error)
	gate          Gate
	resultFilter  ResultFilter
	client        func(msgs []Message) (*ai.Client, error)
	callOptions   func() []ai.Option
	contextBudget func() int
	inbox         chan Inbound
	outbox        chan Event
	onEvent       func(Event)

	// inner holds the conversation and runs one exchange at a time.
	inner *sdkagent.Agent

	// measured is the last reply's real prompt size; see promptTokens.
	measured promptMeasure

	// wake tells an idle Run that Append queued something. Messages
	// themselves wait in inner's queue, which lands them at the next step
	// boundary, or opens the next exchange with them.
	wake chan struct{}

	closed atomic.Bool // guards outbox writes after close

	// turn is the in-flight ThinkAct, so InterruptCurrentTurn can cancel it and
	// wait for it to unwind. A pointer, so swap-with-nil is an atomic claim and
	// concurrent interrupts become no-ops.
	turn atomic.Pointer[turnHandle]

	// interruptPending latches an interrupt that arrived while Run was
	// between iterations (turn pointer was momentarily nil). The next
	// inner-loop iteration checks the latch and bails back to
	// waitForInput rather than starting a new ThinkAct that the user
	// already asked not to run.
	interruptPending atomic.Bool

	// appended counts every Append, and appendedAtInterrupt is that count
	// when the user last interrupted. Messages up to it wait for the next one
	// rather than restarting the agent; any message after it starts a turn
	// however the interrupted turn's unwinding interleaves with it.
	appended, appendedAtInterrupt atomic.Int64
}

// turnHandle binds the per-turn cancel function to a done channel so an
// outside caller (Task.InterruptTurn) can both cancel the turn and wait
// for ThinkAct to actually unwind before resuming work that depends on
// the agent goroutine being quiescent (e.g. clearing pendingPermRequest
// without racing an in-flight PermissionFunc write).
type turnHandle struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (a *agent) ID() string            { return a.id }
func (a *agent) System() System        { return a.system }
func (a *agent) Tools() *Tools         { return a.tools }
func (a *agent) Inbox() chan<- Inbound { return a.inbox }
func (a *agent) Outbox() <-chan Event  { return a.outbox }
func (a *agent) Messages() []Message   { return a.inner.Messages() }
func (a *agent) SetMessages(msgs []Message) {
	a.measured.reset()
	a.inner.SetMessages(msgs)
}

// Append queues a message for the conversation: mid-exchange it enters at the
// next step boundary, otherwise the next exchange opens with it. Safe from any
// goroutine and never blocks.
func (a *agent) Append(msg Message) {
	a.inner.AddMessages(msg)
	a.appended.Add(1)
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

func (a *agent) Run(ctx context.Context) error {
	a.emit(ctx, AgentStarted{})

	var runErr error
	defer func() {
		// StopEvent must be delivered even on context cancellation,
		// so use emitFinal which bypasses ctx.Done().
		a.emitFinal(AgentStopped{Err: runErr})
		a.closed.Store(true)

		if a.outbox != nil {
			close(a.outbox)
		}
	}()

	for {
		glog.Logger().Sugar().Debugf("agent.Run: waitForInput blocking...")
		if err := a.waitForInput(ctx); err != nil {
			if err == errStopped {
				return nil
			}
			runErr = err
			return err
		}
		glog.Logger().Sugar().Debugf("agent.Run: waitForInput received message")

		// A fresh message supersedes a latched interrupt: an Esc that landed
		// while the agent sat idle here had no turn to stop, and keeping the
		// latch made runOneTurn swallow this message.
		a.interruptPending.Store(false)

		// An exchange ends only with nothing queued, so another is owed only
		// for a message that arrived after it had ended.
		for a.inner.Pending() > 0 {
			glog.Logger().Sugar().Debugf("agent.Run: starting ThinkAct")
			result, err, interrupted := a.runOneTurn(ctx)
			if interrupted {
				glog.Logger().Sugar().Debugf("agent.Run: interrupt latched, resuming wait")
				break
			}

			// A failed turn (StopError) is an agent stop, not a turn boundary:
			// emitting TurnEnded would fire OnTurnEnd on top of OnAgentStop.
			// Cancellation still emits (OnTurnEnd guards StopCanceled).
			if result != nil && result.StopReason != StopError {
				glog.Logger().Sugar().Debugf("agent.Run: ThinkAct done, emitting TurnEnded")
				a.emit(ctx, TurnEnded{Result: *result})
			}
			if err != nil {
				glog.Logger().Sugar().Debugf("agent.Run: ThinkAct error: %v", err)
				if err == errStopped {
					return nil
				}
				// Turn-only interrupt: parent ctx still alive, the turn's ctx
				// was cancelled by InterruptCurrentTurn. Just bail back to
				// waitForInput — ai.Repair strips any orphaned tool_use blocks
				// left in the conversation.
				if ctx.Err() == nil && errors.Is(err, context.Canceled) {
					glog.Logger().Sugar().Debugf("agent.Run: turn interrupted by user, resuming wait")
					// Consume the latch that triggered this cancel so the
					// next user message can start a fresh turn. Narrow
					// race: a brand-new Interrupt that arrives between
					// close(h.done) and this Store can be clobbered; the
					// 2nd Esc is treated as a duplicate of the first.
					a.interruptPending.Store(false)
					break
				}
				runErr = err
				return err
			}
		}
	}
}

// runOneTurn runs a single ThinkAct under a per-turn cancellable ctx and
// returns whether an interrupt was latched (instead of running the turn).
//
// The latch is checked AFTER publishing turn=h so that any concurrent
// InterruptCurrentTurn is honored exactly once:
//   - If InterruptCurrentTurn ran BEFORE our Store, its Swap saw turn=nil
//     and set interruptPending=true; our post-Store Swap reads it.
//   - If InterruptCurrentTurn ran AFTER our Store, its Swap saw turn=h
//     and cancelled turnCtx; ThinkAct exits via context.Canceled.
//
// Cleanup (detach + close(done)) is deferred so a panic in ThinkAct still
// releases turnCtx and signals waiters in Task.InterruptTurn.
func (a *agent) runOneTurn(ctx context.Context) (*Result, error, bool) {
	turnCtx, turnCancel := context.WithCancel(ctx)
	h := &turnHandle{cancel: turnCancel, done: make(chan struct{})}
	a.turn.Store(h)
	defer func() {
		if a.turn.CompareAndSwap(h, nil) {
			turnCancel()
		}
		close(h.done)
	}()

	if a.interruptPending.Swap(false) {
		return nil, nil, true
	}

	result, err := a.ThinkAct(turnCtx)
	return result, err, false
}

// InterruptCurrentTurn cancels the ctx of the currently-running ThinkAct
// without ending Run. Returns a channel that closes when the in-flight
// ThinkAct has fully unwound — callers that need to observe a quiescent
// agent (e.g. before mutating shared state that the agent goroutine
// might also touch) should wait on the channel.
//
// When called between turns (turn pointer is nil), latches the
// interrupt so the next inner-loop iteration bails before starting a
// fresh ThinkAct, and returns an already-closed channel.
func (a *agent) InterruptCurrentTurn() <-chan struct{} {
	a.appendedAtInterrupt.Store(a.appended.Load())
	a.interruptPending.Store(true)
	if h := a.turn.Swap(nil); h != nil {
		h.cancel()
		return h.done
	}
	closed := make(chan struct{})
	close(closed)
	return closed
}

var errStopped = errors.New("stopped")

// TruncatedResumePrompt is injected when generation stops at the output limit
// and the caller wants the model to continue in the next turn.
const TruncatedResumePrompt = "Your response was truncated due to output token limits. Resume directly from where you left off. Do not repeat any content."

// waitForInput blocks until a queued message is owed an exchange. Signals are
// handled on the way: SigCompact compacts in place without starting a turn, so
// a manual /compact while idle spends no inference on the lone summary.
func (a *agent) waitForInput(ctx context.Context) error {
	for {
		select {
		case <-a.wake:
			// A wake for a message an exchange already took, or one the
			// user interrupted, is stale.
			if a.inner.Pending() > 0 && a.appended.Load() > a.appendedAtInterrupt.Load() {
				return nil
			}
		case in, ok := <-a.inbox:
			if !ok || in.Signal == SigStop {
				return errStopped
			}
			if in.Signal == SigCompact {
				a.applyCompaction(ctx, in.Summary, len(a.Messages()), "manual")
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// emit sends an event to the outbox for external observation.
// No-op when outbox is nil (subagent direct path).
// Blocks if outbox is full (backpressure). Skips if outbox is closed or ctx is cancelled.
func (a *agent) emit(ctx context.Context, event Event) {
	if a.onEvent != nil {
		a.onEvent(event)
	}
	if a.outbox == nil || a.closed.Load() {
		return
	}
	select {
	case a.outbox <- event:
	case <-ctx.Done():
	}
}

// emitTelemetry delivers a fire-and-forget event: synchronously to onEvent,
// non-blocking to the outbox (dropped if full). Used for events whose
// consumers tolerate misses (system changes, hot-path tracing) and which can
// fire from goroutines without a useful ctx (e.g. system observer callbacks).
func (a *agent) emitTelemetry(event Event) {
	if a.onEvent != nil {
		a.onEvent(event)
	}
	if a.outbox == nil || a.closed.Load() {
		return
	}
	select {
	case a.outbox <- event:
	default:
	}
}

// emitFinal sends a critical event that must be delivered even on ctx cancellation.
// Used for StopEvent — consumers rely on it for cleanup/session saving.
// No-op when outbox is nil. Blocks up to 5 seconds; logs a warning if delivery fails.
func (a *agent) emitFinal(event Event) {
	if a.onEvent != nil {
		a.onEvent(event)
	}
	if a.outbox == nil || a.closed.Load() {
		return
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case a.outbox <- event:
	case <-timer.C:
		log.Printf("core/agent: failed to deliver %T (outbox full for 5s)", event)
	}
}

// ThinkAct runs one exchange and reports what it produced. The Result is
// folded out of the event stream rather than tracked alongside it, so what an
// observer sees and what this returns cannot disagree about one turn.
func (a *agent) ThinkAct(ctx context.Context) (*Result, error) {
	// The prompt and the toolset are read afresh every exchange: both are
	// registries the application mutates while the agent runs.
	a.inner.SetSystem(a.system.Prompt())
	a.inner.SetTools(a.offered()...)

	out := &Result{}
	var turnErr error

	for event, err := range a.inner.Run(ctx) {
		if err != nil {
			// Outside a turn — ErrBusy today, which means two callers drove
			// one conversation and the second must not silently do nothing.
			return nil, err
		}
		a.fold(ctx, event, out)
		if end, ok := event.(sdkagent.TurnEnd); ok {
			turnErr = end.Err
		}
	}

	out.Messages = a.Messages()
	return out, turnErr
}

// fold puts the loop's event on the outbox, as it arrived, and folds it into
// the outcome.
func (a *agent) fold(ctx context.Context, event sdkagent.Event, out *Result) {
	switch e := event.(type) {
	case sdkagent.MessageEnd:
		if e.Err == nil && e.Response != nil {
			out.Steps++
			out.Usage.Add(e.Response.Usage)
		}

	case sdkagent.ToolStart:
		out.ToolUses++

	case sdkagent.TurnEnd:
		out.Content = e.Message.Text()
		out.StopReason = e.StopReason
		if e.Err != nil {
			out.StopDetail = e.Err.Error()
		}
	}
	a.emit(ctx, event)
}

// hooks is where San gets between the SDK's loop and the model: which client
// answers this turn and what options ride with it, whether a tool may run at
// all and what its result is reported as, and — in compact.go, since the
// answer is the same one /compact gives — when the conversation has grown past
// what can be sent.
//
// The two the application may not have are handed over as they are. A nil
// field is a hook the loop does not call; wrapping one to check for nil would
// install a hook that always passes, which is a call per tool to learn there
// was nothing to ask.
func (a *agent) hooks() sdkagent.Hook {
	return sdkagent.Hook{
		PreInfer:     a.preInfer,
		PostInfer:    a.postInfer,
		PreTool:      a.gate,
		PostTool:     a.resultFilter,
		PreStep:      a.preStep,
		OnInferError: a.onInferError,
	}
}

// preInfer points the call at the client this turn's messages ask for, and
// layers on whatever options the application wants with it.
//
// The client is asked for per call rather than held on the agent because one
// vendor's headers depend on what the turn sends — an interactive endpoint
// bills a user-initiated turn differently from an agent-initiated one.
func (a *agent) preInfer(_ context.Context, inf *sdkagent.Inference) error {
	if a.client != nil {
		client, err := a.client(inf.Messages)
		if err != nil {
			return fmt.Errorf("reaching the model: %w", err)
		}
		inf.Client = client
	}
	if a.callOptions != nil {
		inf.Options = append(inf.Options, a.callOptions()...)
	}
	a.measured.sending(len(inf.Messages))
	return nil
}

// postInfer records what the provider counted for the call just made.
func (a *agent) postInfer(_ context.Context, resp *ai.Response) error {
	a.measured.answered(resp.Usage)
	return nil
}
