package app

import (
	"context"
	"strings"
	"testing"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
)

// A background task's tracker item must exist before the task can finish.
//
// The item used to be built from the launching tool's result, which reaches
// the UI goroutine well after the task is already running. A task that ended
// first — a subagent rejected by its provider, a bash command that exits at
// once — completed an item that did not exist yet, so the completion was
// dropped and the item created afterwards named a task that had already
// ended. It stayed in_progress for the rest of the session: rendered
// [stalled] forever, and never letting the tracker panel reset.
//
// Registering the task is now what creates the item, and the manager
// announces that from inside RegisterTask — before the caller has a goroutine
// that could complete it — so the ordering holds by construction.
func TestWorkerItemSurvivesImmediateCompletion(t *testing.T) {
	todo.Initialize()
	t.Cleanup(func() {
		todo.Default().Reset()
		task.SetLifecycleHandler(nil)
	})

	m := &model{services: services{Tracker: todo.Default()}}
	m.wireTaskLifecycle(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	worker := task.Default().CreateAgentTask("bg-1", "Explore", "Audit deps", ctx, cancel)
	worker.Complete(nil)

	items := todo.Default().List()
	if len(items) != 1 {
		t.Fatalf("expected 1 tracker item, got %d", len(items))
	}
	if items[0].Status != todo.StatusCompleted {
		t.Fatalf("status = %q, want %q — the item was stranded", items[0].Status, todo.StatusCompleted)
	}
}

func TestCompletedWorkerWaitsForVisibleNotice(t *testing.T) {
	for _, tc := range []struct {
		name      string
		streaming bool
	}{{"during tool execution", false}, {"during streaming", true}} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := noticeDeliveryModel(t)
			m.env.Width = 80
			m.services.Task = task.NewManager()
			m.mainNotices = make(chan mainNotice, 1)
			m.wireTaskLifecycle(nil)
			broker.Register(broker.Main, func(msg broker.Message) bool { return m.notifyMain(fromBrokerMessage(msg)) })
			t.Cleanup(func() { task.SetLifecycleHandler(nil); broker.Unregister(broker.Main) })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			worker := m.services.Task.CreateAgentTask("t1", "Backend", "research", ctx, cancel)
			worker.Complete(nil)

			assertRetained := func() {
				t.Helper()
				if view := m.renderTrackerList(); !strings.Contains(view, "Backend: research") || !strings.Contains(view, "[completed]") {
					t.Fatalf("worker disappeared before its notice was visible:\n%s", view)
				}
			}
			assertRetained()
			m.conv.Stream.Active = true
			if tc.streaming {
				owner := m.conv.Append(core.ChatMessage{Role: core.ChatAssistant, Content: "Before "})
				m.conv.Stream.MessageID = owner.ID
			} else {
				m.conv.Append(core.ChatMessage{Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: "c1"}})
			}
			m.deliverNotice(<-m.mainNotices)
			if tc.streaming {
				assertNoticeShown(t, m)
				m.conv.AppendStream("after", "")
				if got := m.conv.Messages[0].Content; got != "Before after" {
					t.Fatalf("completion displaced streaming output: %q", got)
				}
				m.conv.Append(core.ChatMessage{Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: "c1"}})
				m.OnStepEnd()
			}
			assertNoticeShown(t, m)
			if view := m.renderTrackerList(); view != "" {
				t.Fatalf("worker remained after its completion notice was shown:\n%s", view)
			}
			if items := m.services.Tracker.List(); len(items) != 1 || items[0].Status != todo.StatusCompleted {
				t.Fatal("hiding a worker must preserve its completed record")
			}
		})
	}
}

func TestTurnEndKeepsWorkerAwaitingCompletionNotice(t *testing.T) {
	m, _ := noticeDeliveryModel(t)
	m.services.Hook = hook.NewEngine(setting.NewData(), "", t.TempDir(), "")
	m.env.Width = 80
	m.services.Task = task.NewManager()
	m.wireTaskLifecycle(nil)
	t.Cleanup(func() { task.SetLifecycleHandler(nil) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := m.services.Task.CreateAgentTask("pending-notice", "Backend", "research", ctx, cancel)
	worker.Complete(nil)

	m.OnTurnEnd(core.Result{StopReason: core.StopEndTurn})
	if items := m.services.Tracker.List(); len(items) != 1 {
		t.Fatal("turn end discarded a worker before its completion notice arrived")
	}
	if view := m.renderTrackerList(); !strings.Contains(view, "Backend: research") {
		t.Fatalf("turn end hid the worker before its completion notice arrived:\n%s", view)
	}
	msg, ok := taskCompletionMessage(worker.GetStatus())
	if !ok {
		t.Fatal("completed worker produced no notice")
	}
	m.deliverNotice(fromBrokerMessage(msg))
	m.OnTurnEnd(core.Result{StopReason: core.StopEndTurn})
	assertNoticeShown(t, m)
	if len(m.services.Tracker.List()) != 0 {
		t.Fatal("announced completions should allow the normal turn-end reset")
	}
}

func TestResumedCompletedWorkerDoesNotWaitForLiveNotice(t *testing.T) {
	m := workflowInputModel(task.NewManager())
	worker := m.services.Tracker.Create("Already finished", "", "", map[string]any{"background_task_id": "previous-process"})
	if err := m.services.Tracker.Update(worker.ID, todo.WithStatus(todo.StatusCompleted)); err != nil {
		t.Fatal(err)
	}
	if view := m.renderTrackerList(); view != "" {
		t.Fatalf("resumed completion waited for a notice that cannot arrive:\n%s", view)
	}
}
