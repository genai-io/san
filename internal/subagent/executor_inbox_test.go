package subagent

import (
	"context"
	"errors"
	"testing"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/task"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

// boundaryAgent delivers mail after the SDK's last pending check, optionally
// settling it into history before returning, as the SDK's finalizer does.
type boundaryAgent struct {
	core.Agent
	t                 *testing.T
	route             string
	settled           bool
	failure           error
	pending, messages []core.Message
	calls, remaining  int
}

func (a *boundaryAgent) Append(m core.Message)    { a.pending = append(a.pending, m) }
func (a *boundaryAgent) Pending() int             { return len(a.pending) }
func (a *boundaryAgent) Messages() []core.Message { return a.messages }
func (a *boundaryAgent) SetMaxSteps(n int)        { a.remaining = n }
func (a *boundaryAgent) ThinkAct(ctx context.Context) (*core.Result, error) {
	a.messages = append(a.messages, a.pending...)
	a.pending = nil
	if ctx.Err() != nil {
		return &core.Result{StopReason: core.StopCanceled}, ctx.Err()
	}
	a.calls++
	a.messages = append(a.messages, core.AssistantMessage("answer", "", nil))
	if a.calls == 1 {
		if !broker.Send(broker.Message{To: a.route, Content: "late accepted input"}) {
			a.t.Fatal("boundary message rejected")
		}
		if a.settled {
			a.messages = append(a.messages, a.pending...)
			a.pending = nil
		}
	}
	reason := core.StopEndTurn
	if a.failure != nil {
		reason = core.StopError
	}
	return &core.Result{Steps: 1, StopReason: reason, Messages: a.messages}, a.failure
}

func TestInboxCompletionBoundary(t *testing.T) {
	for _, settled := range []bool{false, true} {
		for _, budget := range []int{1, 2} {
			a := &boundaryAgent{t: t, route: task.NewID(), settled: settled}
			result, err := runWithInbox(context.Background(), a, a.route, budget)
			if err != nil || a.calls != budget || result.Steps != budget || a.Pending() != 0 {
				t.Fatalf("settled=%v budget=%d result=%+v calls=%d pending=%d err=%v", settled, budget, result, a.calls, a.Pending(), err)
			}
			if budget == 1 && result.StopReason != core.StopMaxSteps {
				t.Fatal("late mail escaped step budget")
			}
			if budget == 2 && (a.remaining != 1 || a.messages[len(a.messages)-2].Text() != "late accepted input") {
				t.Fatal("late input was not answered with remaining budget")
			}
			if budget == 1 && a.messages[len(a.messages)-1].Text() != "late accepted input" {
				t.Fatal("unanswered input was not preserved")
			}
			if broker.Send(broker.Message{To: a.route, Content: "after close"}) {
				t.Fatal("closed route accepted input")
			}
		}
	}
}

func TestInboxFailurePreservesAcceptedInput(t *testing.T) {
	failure := errors.New("provider failed")
	a := &boundaryAgent{t: t, route: task.NewID(), failure: failure}
	result, err := runWithInbox(context.Background(), a, a.route, 3)
	if !errors.Is(err, failure) || result.StopReason != core.StopError || a.calls != 1 || a.Pending() != 0 || a.messages[len(a.messages)-1].Text() != "late accepted input" {
		t.Fatalf("failure lost input or retried inference: %+v %v", result, err)
	}
}

func TestInboxHandlesActualSDKTurnEndDelivery(t *testing.T) {
	for _, budget := range []int{1, 2} {
		route := task.NewID()
		driver := aitest.New(aitest.Says("first"), aitest.Says("second"))
		sent := false
		a := core.NewAgent(core.Config{
			Client: func([]core.Message) (*ai.Client, error) { return driver.Client(), nil },
			System: core.NewSystem(), Tools: core.NewTools(), MaxSteps: budget, OutboxBuf: -1,
			OnEvent: func(event core.Event) {
				if _, ok := event.(sdkagent.TurnEnd); ok && !sent {
					sent = true
					if !broker.Send(broker.Message{To: route, Content: "input after SDK final settlement"}) {
						t.Error("final-boundary input rejected")
					}
				}
			},
		})
		a.Append(core.UserMessage("initial task", nil))
		result, err := runWithInbox(context.Background(), a, route, budget)
		if err != nil || result.Steps != budget || a.Pending() != 0 {
			t.Fatalf("budget=%d result=%+v err=%v", budget, result, err)
		}
		if budget == 2 && result.Content != "second" {
			t.Fatal("late accepted input was not answered")
		}
		if budget == 1 && (result.StopReason != core.StopMaxSteps || a.Messages()[len(a.Messages())-1].Text() != "input after SDK final settlement") {
			t.Fatal("budget exhaustion discarded accepted input")
		}
	}
}
