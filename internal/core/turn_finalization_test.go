package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestCancelTurnAcrossPublication(t *testing.T) {
	for range 100 {
		ag := NewAgent(Config{Client: testClient(aitest.Always(aitest.Hangs())), System: NewSystem(), Tools: NewTools(), OutboxBuf: -1}).(*agent)
		previousDone := make(chan struct{})
		close(previousDone)
		previous := newTurnFinalization(context.Background(), previousDone)
		ag.finalizing.Store(&turnHandle{done: previousDone, finalization: previous})
		ag.Append(UserMessage("next", nil))
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		start := make(chan struct{})
		captured := make(chan *TurnFinalization, 1)
		go func() { <-start; captured <- ag.CancelTurn() }()
		close(start)
		result, _, interrupted := ag.runOneTurn(ctx)
		receipt := <-captured
		cancel()
		if interrupted {
			if receipt != previous {
				t.Fatal("idle latch captured successor")
			}
		} else if result == nil || receipt != result.Finalization || receipt == previous {
			t.Fatal("cancelled successor but returned previous receipt")
		}
	}
}

func TestTurnFinalizationRequiresExecutionAndHandling(t *testing.T) {
	for _, handlingFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "execution-first", true: "handling-first"}[handlingFirst], func(t *testing.T) {
			execution := make(chan struct{})
			f := newTurnFinalization(context.Background(), execution)
			writeErr := errors.New("write failed")
			f.RecordError(writeErr)
			if handlingFirst {
				f.Acknowledge(nil)
			} else {
				close(execution)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			if err := f.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("premature completion: %v", err)
			}
			if handlingFirst {
				close(execution)
			} else {
				f.Acknowledge(nil)
			}
			f.Acknowledge(errors.New("duplicate"))
			late := errors.New("late producer error")
			f.RecordError(late)
			if err := f.Wait(context.Background()); errors.Is(err, late) {
				t.Fatal("settled result mutated")
			}
			if err := f.Wait(context.Background()); !errors.Is(err, writeErr) {
				t.Fatalf("lost write error: %v", err)
			}
		})
	}
}

func TestCancelTurnFinalizationExactEvent(t *testing.T) {
	llm := aitest.Always(aitest.Hangs())
	ag := NewAgent(Config{ID: "test", Client: testClient(llm), System: NewSystem(), Tools: NewTools()})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- ag.Run(ctx) }()
	terminals := make(chan Result, 2)
	go func() {
		for ev := range ag.Outbox() {
			if end, ok := ev.(TurnEnded); ok {
				terminals <- end.Result
			}
		}
	}()
	ag.Append(UserMessage("first", nil))
	waitFor(t, "first inference", func() bool { return llm.Calls() == 1 })
	first := ag.CancelTurn()
	if first == nil || ag.CancelTurn() != first {
		t.Fatal("repeated cancel changed receipt")
	}
	var end Result
	select {
	case end = <-terminals:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if end.Finalization != first {
		t.Fatal("terminal event lost identity")
	}
	short, stop := context.WithTimeout(ctx, 10*time.Millisecond)
	defer stop()
	if err := first.Wait(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unhandled terminal succeeded: %v", err)
	}
	ag.Append(UserMessage("second", nil))
	waitFor(t, "second inference", func() bool { return llm.Calls() == 2 })
	second := ag.CancelTurn()
	if second == first {
		t.Fatal("successor reused receipt")
	}
	first.Acknowledge(nil)
	if err := first.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-terminals:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	short2, stop2 := context.WithTimeout(ctx, 10*time.Millisecond)
	defer stop2()
	if err := second.Wait(short2); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stale acknowledgment settled successor: %v", err)
	}
	cancel()
	if err := second.Wait(context.Background()); err == nil {
		t.Fatal("shutdown acknowledged missing handling")
	}
	<-runDone
}

func TestCancelTurnFinalizationWaitsForProducerWrite(t *testing.T) {
	llm := aitest.Always(aitest.Hangs())
	writing, release := make(chan struct{}), make(chan struct{})
	writeErr := errors.New("native record write failed")
	ag := NewAgent(Config{ID: "test", Client: testClient(llm), System: NewSystem(), Tools: NewTools(), OnEvent: func(ev Event) {
		if end, ok := ev.(TurnEnded); ok {
			close(writing)
			<-release
			end.Result.Finalization.RecordError(writeErr)
		}
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- ag.Run(ctx) }()
	go func() {
		for ev := range ag.Outbox() {
			if end, ok := ev.(TurnEnded); ok {
				end.Result.Finalization.Acknowledge(nil)
			}
		}
	}()
	ag.Append(UserMessage("first", nil))
	waitFor(t, "inference", func() bool { return llm.Calls() == 1 })
	f := ag.CancelTurn()
	select {
	case <-writing:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	short, stop := context.WithTimeout(ctx, 10*time.Millisecond)
	defer stop()
	if err := f.Wait(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked write completed: %v", err)
	}
	close(release)
	if err := f.Wait(ctx); !errors.Is(err, writeErr) {
		t.Fatalf("native write error lost: %v", err)
	}
	cancel()
	<-runDone
}

func TestNaturalRunShutdownReleasesUnacknowledgedReceipt(t *testing.T) {
	for _, closeInbox := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "closed-inbox"}[closeInbox], func(t *testing.T) {
			llm := aitest.Always(aitest.Hangs())
			ag := NewAgent(Config{Client: testClient(llm), System: NewSystem(), Tools: NewTools()}).(*agent)
			done := make(chan error, 1)
			go func() { done <- ag.Run(context.Background()) }()
			ag.Append(UserMessage("work", nil))
			waitFor(t, "inference", func() bool { return llm.Calls() == 1 })
			receipt := ag.CancelTurn()
			if closeInbox {
				close(ag.inbox)
			} else {
				ag.Inbox() <- Inbound{Signal: SigStop}
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("Run did not stop")
			}
			if err := receipt.Wait(context.Background()); err == nil {
				t.Fatal("missing handling succeeded")
			}
		})
	}
}

func TestCancelAfterNaturalCompletionCapturesSameReceipt(t *testing.T) {
	llm := aitest.Always(aitest.Says("done"))
	ag := NewAgent(Config{Client: testClient(llm), System: NewSystem(), Tools: NewTools()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ag.Run(ctx) }()
	ag.Append(UserMessage("work", nil))
	for ev := range ag.Outbox() {
		if end, ok := ev.(TurnEnded); ok {
			f := ag.CancelTurn()
			if f != end.Result.Finalization {
				t.Fatal("wrong receipt after execution")
			}
			f.Acknowledge(ErrTurnCompleted)
			if err := f.Wait(ctx); err != ErrTurnCompleted {
				t.Fatalf("outcome = %v", err)
			}
			break
		}
	}
	cancel()
	<-done
}

func TestTurnFinalizationShutdownReleasesAllWaiters(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	execution := make(chan struct{})
	f := newTurnFinalization(ctx, execution)
	results := make(chan error, 8)
	for range 8 {
		go func() { results <- f.Wait(context.Background()) }()
	}
	cancel()
	for range 8 {
		select {
		case err := <-results:
			if err == nil {
				t.Fatal("false success")
			}
		case <-time.After(time.Second):
			t.Fatal("lost waiter")
		}
	}
}
