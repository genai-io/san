package core

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestInterruptCompletionWaitsForToolButNotTurnEnded(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	boundary, drain := make(chan struct{}), make(chan struct{})
	var releaseOnce, drainOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	defer drainOnce.Do(func() { close(drain) })
	var ran, ranOnDead atomic.Int32
	driver := aitest.New(aitest.Asks(ToolCall{ID: "work", Name: "work", Input: "{}"}), aitest.Hangs())
	ag := NewAgent(Config{
		ID: "test", Client: testClient(driver), System: NewSystem(),
		Tools: NewTools(cancelOnRunTool{name: "work", ran: &ran, ranOnDead: &ranOnDead, onRun: func() {
			close(entered)
			<-release // simulate slow cleanup before the tool returns
		}}),
		OnEvent: func(ev Event) {
			if e, ok := ev.(TurnEnded); ok && e.Result.StopReason == StopCanceled {
				select {
				case <-boundary:
				default:
					close(boundary)
					<-drain
				}
			}
		},
	}).(*agent)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	runDone := make(chan struct{})
	go func() { _ = ag.Run(ctx); close(runDone) }()
	go func() {
		for range ag.Outbox() {
		}
	}()
	ag.Append(UserMessage("first", nil))
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("tool did not start")
	}
	first := ag.InterruptCurrentTurn()
	second := ag.InterruptCurrentTurn()
	if first != second {
		t.Fatal("repeated interrupt lost the unwinding turn")
	}
	select {
	case <-first:
		t.Fatal("completion preceded tool return")
	default:
	}
	immediate := UserMessage("accepted during unwind", nil)
	immediate.ID = "immediate-input"
	ag.Append(immediate)
	if driver.Calls() != 1 {
		t.Fatal("new input overlapped the unwinding execution")
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-first:
	case <-ctx.Done():
		t.Fatal("execution did not complete")
	}
	select {
	case <-boundary:
	case <-ctx.Done():
		t.Fatal("TurnEnded did not arrive")
	}
	count := 0
	for _, msg := range ag.Messages() {
		if msg.ID == immediate.ID {
			count++
			if msg.Text() != immediate.Text() {
				t.Fatal("accepted input content changed")
			}
		}
	}
	if count != 1 {
		t.Fatalf("accepted input appears %d times in native history", count)
	}
	// The completion is closed even though the synchronous TurnEnded observer
	// has not returned. No application/persistence acknowledgement is implied.
	ag.Append(UserMessage("successor", nil))
	drainOnce.Do(func() { close(drain) })
	waitFor(t, "successor inference", func() bool { return driver.Calls() == 2 })
	successor := ag.turn.Load()
	if successor == nil {
		t.Fatal("successor not running")
	}
	<-first // observing an old completion has no effect on the successor
	if ag.turn.Load() != successor {
		t.Fatal("old completion affected successor")
	}
	third := ag.InterruptCurrentTurn()
	if third == first {
		t.Fatal("successor reused old completion")
	}
	select {
	case <-third:
	case <-ctx.Done():
		t.Fatal("successor did not unwind")
	}
	cancel()
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
}
