package agent

import (
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
)

func TestRuntimeInterruptInactive(t *testing.T) {
	runtime := &Runtime{}
	select {
	case <-runtime.InterruptCurrentTurn():
	default:
		t.Fatal("inactive completion is not closed")
	}
	if runtime.Active() {
		t.Fatal("interrupt activated runtime")
	}
	runtime.InterruptTurn()
}

func TestRuntimeInterruptCompletionAndBoundedWait(t *testing.T) {
	ag := newControlledAgent(false)
	done := make(chan struct{})
	calls := 0
	ag.interrupt = func() <-chan struct{} { calls++; return done }
	runtime := sessionWithAgents(ag)
	if err := runtime.Start(BuildParams{}, nil); err != nil {
		t.Fatal(err)
	}
	defer runtime.Stop()
	<-ag.started
	first := runtime.InterruptCurrentTurn()
	if calls != 1 || first != done {
		t.Fatal("cancellation was not bound synchronously")
	}
	select {
	case <-first:
		t.Fatal("completion closed before unwind")
	default:
	}
	if again := runtime.InterruptCurrentTurn(); again != first {
		t.Fatal("completion not forwarded")
	}
	// The legacy UI path must return even when execution remains alive.
	started := time.Now()
	runtime.InterruptTurn()
	if elapsed := time.Since(started); elapsed < interruptDrainTimeout || elapsed > 2*time.Second {
		t.Fatalf("bounded wait took %s", elapsed)
	}
	select {
	case <-first:
		t.Fatal("bounded wait fabricated completion")
	default:
	}
	msg := core.UserMessage("immediate next input", nil)
	msg.ID = "accepted-input"
	if err := runtime.Send(msg); err != nil {
		t.Fatal(err)
	}
	if got := <-ag.appended; got.ID != msg.ID || got.Text() != msg.Text() {
		t.Fatal("input identity changed")
	}
	close(done)
	<-first
	if !runtime.Active() {
		t.Fatal("turn completion ended runtime")
	}
}
