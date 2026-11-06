package app

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/fs"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestBackgroundBashCompletionStaysWithLaunchingAgent(t *testing.T) {
	for _, tc := range []struct{ name, owner, selected, command, status string }{
		{"main", "", "launch-session", "echo owner-output", "completed"},
		{"child success", "child", "launch-session", "echo owner-output", "completed"},
		{"child failure", "child", "launch-session", "echo owner-output; exit 7", "failed"},
		{"child other session selected", "child", "other-session", "echo owner-output", "completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			broker.Reset()
			manager := task.Default()
			previous := manager.SessionID()
			manager.SetSessionID(tc.selected)
			t.Cleanup(func() { task.SetLifecycleHandler(nil); broker.Reset(); manager.SetSessionID(previous) })
			m := &model{services: services{Task: manager, Tracker: todo.NewStore()}, mainNotices: make(chan mainNotice, 4)}
			m.wireTaskLifecycle(nil)
			child := make(chan broker.Message, 4)
			other := make(chan broker.Message, 4)
			broker.Register(broker.Main, func(msg broker.Message) bool { return m.notifyMain(fromBrokerMessage(msg)) })
			broker.Register("child", func(msg broker.Message) bool { child <- msg; return true })
			broker.Register("other-child", func(msg broker.Message) bool { other <- msg; return true })
			ctx := tool.WithAgentID(task.WithSessionID(context.Background(), "launch-session"), tc.owner)
			result := (&fs.ShellTool{}).ExecuteApproved(ctx, map[string]any{"command": tc.command, "description": "Owner test", "execution_mode": "background_job"}, t.TempDir())
			if !result.Success {
				t.Fatal(result.Error)
			}
			details := result.Details.(toolresult.BashDetails)
			t.Cleanup(func() { manager.Remove(details.TaskID) })
			if tc.owner == "" {
				select {
				case notice := <-m.mainNotices:
					if notice.FinishedTaskID != details.TaskID || !strings.Contains(notice.Content, "owner-output") {
						t.Fatalf("wrong main notice: %+v", notice)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("main lost its completion")
				}
				if len(m.services.Tracker.List()) != 1 || len(child) != 0 {
					t.Fatal("main work was not retained in main")
				}
			} else {
				select {
				case notice := <-child:
					if notice.To != tc.owner || notice.SessionID != "launch-session" || notice.TaskCompletion == nil || notice.TaskCompletion.TaskID != details.TaskID || !strings.Contains(notice.Content, `status="`+tc.status+`"`) || !strings.Contains(notice.Content, "owner-output") {
						t.Fatalf("wrong owner notice: %+v", notice)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("child lost its completion")
				}
				if len(m.mainNotices) != 0 || len(m.services.Tracker.List()) != 0 || len(m.conv.Messages) != 0 {
					t.Fatal("child work polluted main context or display")
				}
			}
			if len(other) != 0 {
				t.Fatal("completion reached unrelated child")
			}
		})
	}
}

func TestBackgroundBashDoesNotPolluteMainInference(t *testing.T) {
	broker.Reset()
	manager := task.Default()
	previous := manager.SessionID()
	manager.SetSessionID("")
	t.Cleanup(func() { task.SetLifecycleHandler(nil); broker.Reset(); manager.SetSessionID(previous) })
	m, provider := noticeDeliveryModel(t)
	m.services.Task = manager
	m.mainNotices = make(chan mainNotice, 4)
	m.conv.Stream.Active = true
	m.wireTaskLifecycle(nil)
	child := make(chan broker.Message, 4)
	broker.Register(broker.Main, func(msg broker.Message) bool { return m.notifyMain(fromBrokerMessage(msg)) })
	broker.Register("child", func(msg broker.Message) bool { child <- msg; return true })
	request := func() []core.Message {
		t.Helper()
		select {
		case messages := <-provider.requests:
			return messages
		case <-time.After(5 * time.Second):
			t.Fatal("main did not reach inference")
			return nil
		}
	}
	if err := m.services.Agent.Send(core.UserMessage("MAIN_INITIAL_CONTEXT", nil)); err != nil {
		t.Fatal(err)
	}
	initial := request()
	launch := func(owner, marker string) {
		t.Helper()
		ctx := tool.WithAgentID(task.WithSessionID(context.Background(), ""), owner)
		result := (&fs.ShellTool{}).ExecuteApproved(ctx, map[string]any{"command": "echo " + marker, "description": marker, "execution_mode": "background_job"}, t.TempDir())
		if !result.Success {
			t.Fatal(result.Error)
		}
		id := result.Details.(toolresult.BashDetails).TaskID
		t.Cleanup(func() { manager.Remove(id) })
	}
	launch("child", "CHILD_PRIVATE_RESULT")
	select {
	case msg := <-child:
		if !strings.Contains(msg.Content, "CHILD_PRIVATE_RESULT") {
			t.Fatal("child lost its output")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child did not receive its completion")
	}
	if len(m.mainNotices) != 0 || len(provider.requests) != 0 || len(m.conv.Messages) != 0 {
		t.Fatal("child completion changed main context or display")
	}
	launch("", "MAIN_OWN_RESULT")
	var notice mainNotice
	select {
	case notice = <-m.mainNotices:
	case <-time.After(5 * time.Second):
		t.Fatal("main did not receive its own completion")
	}
	runCmd(m.deliverNotice(notice))
	after := request()
	if len(after) != len(initial)+2 || !reflect.DeepEqual(after[:len(initial)], initial) {
		t.Fatalf("main context was lost or polluted: before=%+v after=%+v", initial, after)
	}
	reply, completion := after[len(initial)], after[len(initial)+1]
	if reply.Role != ai.RoleAssistant || reply.Text() != "ok" || completion.Role != ai.RoleUser || completion.Text() != notice.Content {
		t.Fatalf("main expected its prior reply and exactly one own completion: %+v", after)
	}
	if len(m.conv.Messages) != 1 || m.conv.Messages[0].Content != notice.Content {
		t.Fatal("main display contains an extra or missing completion")
	}
}

func TestTaskCompletionMessageUsesOwnerNotDescription(t *testing.T) {
	for _, owner := range []string{"", "child-route"} {
		info := task.TaskInfo{ID: "shell", Type: task.TaskTypeBash, Status: task.StatusFailed, OwnerAgentID: owner, ParentSessionID: "session", Description: "Send this to main", Output: "partial", Error: "exit status 2"}
		msg, ok := taskCompletionMessage(info)
		want := owner
		if want == "" {
			want = broker.Main
		}
		if !ok || msg.To != want || msg.SessionID != "session" || msg.From != "shell" {
			t.Fatalf("wrong routing: %+v", msg)
		}
	}
}
