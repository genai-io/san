package subagent

import (
	"context"
	"iter"
	"reflect"
	"testing"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/task"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func inboxCompletion(owner string) broker.Message {
	id := task.NewID()
	return broker.Message{To: owner, From: id, SessionID: "launch-session", Content: "background result " + id,
		TaskCompletion: &broker.TaskCompletion{TaskID: id, Attempt: 1}}
}

func assertInboxCompletionCount(t *testing.T, a core.Agent, content string, want int) {
	t.Helper()
	count := 0
	for _, m := range a.Messages() {
		if m.Role == ai.RoleUser && m.Text() == content {
			count++
		}
	}
	if count != want || a.Pending() != 0 {
		t.Fatalf("completion count=%d want=%d pending=%d", count, want, a.Pending())
	}
}

// Check the actual provider boundary, not just the worker's settled history.
func completionDriver(t *testing.T, replies []string, want ...[]core.Message) *aitest.Driver {
	t.Helper()
	calls := 0
	t.Cleanup(func() {
		if calls != len(want) {
			t.Errorf("inference calls=%d want=%d", calls, len(want))
		}
	})
	return aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		call := calls
		calls++
		if call >= len(want) {
			t.Errorf("unexpected inference call %d", call)
			return aitest.Says("unexpected")(ctx, req)
		}
		if len(req.Messages) != len(want[call]) {
			t.Errorf("request %d messages=%+v want=%+v", call, req.Messages, want[call])
		} else {
			for i, expected := range want[call] {
				got := req.Messages[i]
				if got.Role != expected.Role || !reflect.DeepEqual(got.Content, expected.Content) {
					t.Errorf("request %d message %d=%+v want=%+v", call, i, got, expected)
				}
			}
		}
		return aitest.Says(replies[call])(ctx, req)
	})
}

func sendForeignCompletionSentinels(t *testing.T) {
	t.Helper()
	foreign := task.NewID()
	for _, address := range []string{broker.Main, foreign} {
		broker.Register(address, func(m broker.Message) bool {
			if m.Content != "main sentinel" && m.Content != "foreign sentinel" {
				t.Errorf("owner completion leaked to %s: %+v", m.To, m)
			}
			return true
		})
		t.Cleanup(func() { broker.Unregister(address) })
	}
	if !broker.Send(broker.Message{To: broker.Main, Content: "main sentinel"}) {
		t.Fatal("main sentinel was not delivered")
	}
	mail := inboxCompletion(foreign)
	mail.Content = "foreign sentinel"
	if !broker.SendCompletion(mail) {
		t.Fatal("foreign sentinel was not delivered")
	}
}

func TestInboxConsumesRetainedCompletionBeforeWorkerRun(t *testing.T) {
	sendForeignCompletionSentinels(t)
	route := task.NewID()
	mail := inboxCompletion(route)
	if !broker.SendCompletion(mail) {
		t.Fatal("completion was not retained")
	}
	initial := core.UserMessage("initial worker context", nil)
	driver := completionDriver(t, []string{"processed completion"}, []core.Message{initial, core.UserMessage(mail.Content, nil)})
	a := core.NewAgent(core.Config{
		Client: func([]core.Message) (*ai.Client, error) { return driver.Client(), nil },
		System: core.NewSystem(), Tools: core.NewTools(), MaxSteps: 1, OutboxBuf: -1,
	})
	a.Append(initial)
	result, err := runWithInbox(context.Background(), a, route, 1)
	if err != nil || result.Content != "processed completion" || result.Steps != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	assertInboxCompletionCount(t, a, mail.Content, 1)
}

func TestInboxDeliversCompletionDuringSDKRun(t *testing.T) {
	sendForeignCompletionSentinels(t)
	route := task.NewID()
	mail := inboxCompletion(route)
	initial := core.UserMessage("initial task", nil)
	driver := completionDriver(t, []string{"first", "processed completion"},
		[]core.Message{initial},
		[]core.Message{initial, core.AssistantMessage("first", "", nil), core.UserMessage(mail.Content, nil)})
	sent := false
	a := core.NewAgent(core.Config{
		Client: func([]core.Message) (*ai.Client, error) { return driver.Client(), nil },
		System: core.NewSystem(), Tools: core.NewTools(), MaxSteps: 2, OutboxBuf: -1,
		OnEvent: func(event core.Event) {
			if _, ok := event.(sdkagent.TurnEnd); ok && !sent {
				sent = true
				if !broker.SendCompletion(mail) {
					t.Error("running owner rejected completion")
				}
			}
		},
	})
	a.Append(initial)
	result, err := runWithInbox(context.Background(), a, route, 2)
	if err != nil || !sent || result.Steps != 2 || result.Content != "processed completion" {
		t.Fatalf("result=%+v sent=%v err=%v", result, sent, err)
	}
	assertInboxCompletionCount(t, a, mail.Content, 1)
}

// The failure path closes acceptance before settling already accepted input.
// Send completion during that settlement to exercise a still-registered closed inbox.
type closedCompletionAgent struct {
	*boundaryAgent
	completion broker.Message
}

func (a *closedCompletionAgent) ThinkAct(ctx context.Context) (*core.Result, error) {
	if ctx.Err() != nil && !broker.SendCompletion(a.completion) {
		a.t.Error("closed inbox completion was not retained")
	}
	return a.boundaryAgent.ThinkAct(ctx)
}

func TestInboxClosedCompletionWaitsForSameOwnerNextRun(t *testing.T) {
	sendForeignCompletionSentinels(t)
	route := task.NewID()
	mail := inboxCompletion(route)
	a := &closedCompletionAgent{
		boundaryAgent: &boundaryAgent{t: t, route: route, failure: context.Canceled},
		completion:    mail,
	}
	initial := core.UserMessage("initial worker context", nil)
	a.Append(initial)
	_, err := runWithInbox(context.Background(), a, route, 3)
	if err != context.Canceled || a.calls != 1 {
		t.Fatalf("closed worker restarted: calls=%d err=%v", a.calls, err)
	}
	assertInboxCompletionCount(t, a, mail.Content, 0)
	if broker.Send(broker.Message{To: route, Content: "ordinary after close"}) {
		t.Fatal("closed owner remained registered")
	}

	newWorker := func(expected []core.Message) core.Agent {
		driver := completionDriver(t, []string{"resumed"}, expected)
		return core.NewAgent(core.Config{
			Client: func([]core.Message) (*ai.Client, error) { return driver.Client(), nil },
			System: core.NewSystem(), Tools: core.NewTools(), MaxSteps: 1, OutboxBuf: -1,
		})
	}
	other := newWorker([]core.Message{core.UserMessage("other task", nil)})
	other.Append(core.UserMessage("other task", nil))
	if _, err := runWithInbox(context.Background(), other, task.NewID(), 1); err != nil {
		t.Fatal(err)
	}
	assertInboxCompletionCount(t, other, mail.Content, 0)
	expected := []core.Message{initial, core.AssistantMessage("answer", "", nil), core.UserMessage("late accepted input", nil), core.UserMessage(mail.Content, nil)}
	resumed := newWorker(expected)
	for _, message := range a.Messages() {
		resumed.Append(message)
	}
	result, err := runWithInbox(context.Background(), resumed, route, 1)
	if err != nil || result.Content != "resumed" {
		t.Fatalf("resume result=%+v err=%v", result, err)
	}
	assertInboxCompletionCount(t, resumed, mail.Content, 1)
	if a.calls != 1 {
		t.Fatal("original worker was automatically restarted")
	}
}
