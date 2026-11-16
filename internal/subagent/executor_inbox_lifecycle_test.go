package subagent

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestBackgroundInboxReadyBeforeLaunchReturns(t *testing.T) {
	store, parent := continuationSessions(t, "startup-owner")
	source := core.UserMessage("startup message source", nil)
	source.ID = core.NewMessageID()
	if err := parent.Append(context.Background(), session.Event{Payload: session.MessageAppendPayload{Messages: []core.Message{source}}}); err != nil {
		t.Fatal(err)
	}
	snapshot := parent.Snapshot()
	events := snapshot.Events()
	seq := events[len(events)-1].Seq
	driver := aitest.New(aitest.Says("handled startup messages"))
	e := NewExecutor(continuationProvider{driver}, t.TempDir(), "aitest", nil)
	bindContinuationSessions(t, e, store, parent)
	worker, err := e.RunBackground(tool.AgentExecRequest{
		Prompt: "original task",
		OnIdentity: func(identity toolresult.AgentIdentity) {
			for _, text := range []string{"startup one", "startup two"} {
				if !broker.Send(broker.Message{To: identity.ID, Content: text, SourceSession: parent.ID(), SourceSeq: seq}) {
					t.Errorf("message rejected before launch returned: %s", text)
				}
			}
			if broker.Send(broker.Message{To: identity.ID, Content: "invalid source", SourceSession: parent.ID(), SourceSeq: seq + 1000}) {
				t.Error("startup route accepted invalid provenance")
			}
			if driver.Calls() != 0 {
				t.Error("worker ran before launch identity was published")
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { task.Default().Remove(worker.GetID()) })
	if !worker.WaitForCompletion(5 * time.Second) {
		t.Fatal("startup worker did not finish")
	}
	if info := worker.GetStatus(); info.Status != task.StatusCompleted {
		t.Fatalf("worker failed: %+v", info)
	}
	messages := driver.Last().Messages
	if len(messages) != 3 || !strings.Contains(messages[0].Text(), "original task") || messages[1].Text() != "startup one" || messages[2].Text() != "startup two" {
		t.Fatalf("startup input was lost or reordered: %+v", messages)
	}
	projection, err := store.Read(context.Background(), worker.GetStatus().AgentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	active := projection.ActiveMessages()
	if len(active) != len(messages)+1 {
		t.Fatalf("startup input duplicated in native history: %d messages", len(active))
	}
	for i, message := range messages {
		if active[i].ID != message.ID {
			t.Fatalf("startup/native input IDs differ at %d", i)
		}
	}
	sourced := 0
	for _, event := range projection.Events() {
		if event.Source != nil && event.Source.Session == parent.ID() && event.Source.Seq == seq {
			sourced++
		}
	}
	if sourced != 2 {
		t.Fatalf("startup provenance recorded %d times, want 2", sourced)
	}
	if broker.Send(broker.Message{To: worker.GetID(), Content: "after completion"}) {
		t.Fatal("completed worker retained its route")
	}
}

func TestInboxCancelReleasesStartupState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	route := task.NewID()
	inbox := newRunInbox(ctx, route, nil)
	defer inbox.close()
	if !broker.Send(broker.Message{To: route, Content: "queued before initialization"}) {
		t.Fatal("startup inbox rejected mail")
	}
	staleDelivery := inbox.deliver
	cancel()
	inbox.close()
	inbox.close()
	if staleDelivery(broker.Message{Content: "stale callback"}) == nil || broker.Send(broker.Message{To: route}) {
		t.Fatal("canceled inbox accepted mail")
	}
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	if !inbox.closed || inbox.pending != nil || inbox.agent != nil || inbox.owner != nil || inbox.stop != nil || inbox.unregister != nil {
		t.Fatal("inbox retained startup resources after cleanup")
	}
}

func TestInboxPendingLimitRecoversAfterConsumption(t *testing.T) {
	route := task.NewID()
	inbox := newRunInbox(context.Background(), route, nil)
	defer inbox.close()
	for i := range maxPendingWorkerMessages {
		if !broker.Send(broker.Message{To: route, Content: fmt.Sprintf("startup %d", i)}) {
			t.Fatalf("startup message %d rejected", i)
		}
	}
	if err := broker.SendErr(broker.Message{To: route, Content: "overflow"}); !errors.Is(err, broker.ErrInboxFull) {
		t.Fatalf("overflow error = %v, want ErrInboxFull", err)
	}
	driver := aitest.New(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		if len(req.Messages) != maxPendingWorkerMessages+1 {
			t.Errorf("startup batch size = %d", len(req.Messages))
		}
		for i := range maxPendingWorkerMessages {
			if req.Messages[i+1].Text() != fmt.Sprintf("startup %d", i) {
				t.Errorf("startup message %d reordered", i)
			}
		}
		if !broker.Send(broker.Message{To: route, Content: "after consumption"}) {
			t.Error("inbox stayed full after consumption")
		}
		return aitest.Says("first")(ctx, req)
	}, aitest.Says("second"))
	ag := core.NewAgent(core.Config{Client: func([]core.Message) (*ai.Client, error) { return driver.Client(), nil }, System: core.NewSystem(), Tools: core.NewTools(), OutboxBuf: -1, MaxSteps: 2})
	ag.Append(core.UserMessage("initial task", nil))
	result, err := inbox.run(context.Background(), ag, 2, nil)
	if err != nil || result.Content != "second" || result.Steps != 2 || ag.Pending() != 0 {
		t.Fatalf("inbox did not drain: %+v, %v", result, err)
	}
	for _, message := range ag.Messages() {
		if message.Text() == "overflow" {
			t.Fatal("rejected message entered the conversation")
		}
	}
}

func TestBackgroundInboxCleanupBeforeExecution(t *testing.T) {
	for _, cancelLaunch := range []bool{false, true} {
		t.Run(map[bool]string{false: "initialization-failure", true: "canceled-launch"}[cancelLaunch], func(t *testing.T) {
			store, parent := continuationSessions(t, "cleanup-owner")
			driver := aitest.New(aitest.Says("must not infer"))
			e := NewExecutor(continuationProvider{driver}, t.TempDir(), "aitest", nil)
			bindContinuationSessions(t, e, store, parent)
			if !cancelLaunch {
				e.SetChildHooks(func(child *session.Session, _ llm.Provider, _ string) (hook.Handler, func(llm.Provider, string), func()) {
					return childStartHooks{dispatch: func(event hook.EventType) {
						if event == hook.SubagentStart {
							_ = child.Close()
						}
					}}, nil, func() {}
				})
			}
			worker, err := e.RunBackground(tool.AgentExecRequest{Prompt: "work", OnIdentity: func(identity toolresult.AgentIdentity) {
				if !broker.Send(broker.Message{To: identity.ID, Content: "accepted before execution"}) {
					t.Error("startup mail rejected")
				}
				if cancelLaunch {
					worker, found := task.Default().Get(identity.ID)
					if !found {
						t.Error("published task missing")
					} else if err := worker.Stop(); err != nil {
						t.Error(err)
					}
				}
			}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { task.Default().Remove(worker.GetID()) })
			if !worker.WaitForCompletion(5 * time.Second) {
				t.Fatal("worker did not finish cleanup")
			}
			if worker.IsRunning() || driver.Calls() != 0 {
				t.Fatal("failed startup executed inference or remained running")
			}
			if broker.Send(broker.Message{To: worker.GetID(), Content: "after shutdown"}) {
				t.Fatal("failed startup leaked its route")
			}
			child, err := store.Open(context.Background(), worker.GetStatus().AgentSessionID)
			if err != nil {
				t.Fatalf("failed startup leaked its writer claim: %v", err)
			}
			defer child.Close()
			found := false
			snapshot := child.Snapshot()
			for _, message := range snapshot.ActiveMessages() {
				found = found || message.Text() == "accepted before execution"
			}
			if !found {
				t.Fatal("accepted startup input was not preserved")
			}
		})
	}
}
