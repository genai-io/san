package app

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/group"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool/perm"
	"github.com/genai-io/san/tests/integration/testutil"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func switchToOtherSession(t *testing.T, m *model, store *session.Store) {
	t.Helper()
	if err := testutil.ImportSession(store, &session.History{Metadata: session.Metadata{ID: "other"}, Messages: []core.Message{{ID: "other-user", Role: ai.RoleUser, Content: ai.TextContent("other conversation")}}}); err != nil {
		t.Fatal(err)
	}
	if err := m.loadSessionByID("other"); err != nil {
		t.Fatal(err)
	}
}
func TestTemporaryPermissionsStayInOriginalSession(t *testing.T) {
	for _, action := range []string{"clear", "resume", "fork"} {
		t.Run(action, func(t *testing.T) {
			m, store := resumedModel(t, parallelResultChain())
			t.Cleanup(group.Forget)
			policy := &setting.Data{Permissions: setting.PermissionSettings{Ask: []string{"Bash"}}}
			args := map[string]any{"command": "make build"}
			if got := policy.HasPermissionToUseTool("Bash", args, m.env.SessionPermissions); got.Behavior != perm.Prompt {
				t.Fatalf("invalid setup: %+v", got)
			}
			m.env.SessionPermissions.AllowTool("Bash")
			m.env.SessionPermissions.AllowPattern("Bash(make test)")
			if action == "clear" {
				if err := m.clearSession(); err != nil {
					t.Fatal(err)
				}
			} else if action == "fork" {
				if _, err := m.forkSession(); err != nil {
					t.Fatal(err)
				}
			} else {
				switchToOtherSession(t, m, store)
			}
			if got := policy.HasPermissionToUseTool("Bash", args, m.env.SessionPermissions); got.Behavior != perm.Prompt {
				t.Fatalf("new session %s still permits the old session's grant: %+v", m.sessionID(), got)
			}
			if len(m.env.SessionPermissions.Snapshot().AllowedPatterns) != 0 {
				t.Fatal("new session inherited temporary permission patterns")
			}
		})
	}
}
func TestWorkerCompletionStaysInOriginalSession(t *testing.T) {
	m, store := resumedModel(t, parallelResultChain())
	t.Cleanup(group.Forget)
	m.initTaskStorage(m.sessionID())
	m.mainNotices = make(chan mainNotice, 4)
	m.wireTaskLifecycle(nil)
	broker.Register(broker.Main, func(msg broker.Message) bool { return m.notifyMain(fromBrokerMessage(msg)) })
	t.Cleanup(func() { task.SetLifecycleHandler(nil); broker.Unregister(broker.Main) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := m.services.Task.CreateAgentTask("old-worker", "Audit", "old session job", ctx, cancel)
	worker.AppendOutput([]byte("result-only-for-original-session"))
	// A notice can already be queued while the selector switches conversations.
	queued, _ := taskCompletionMessage(task.TaskInfo{ID: "queued", ParentSessionID: m.sessionID(), Status: task.StatusCompleted, Output: "old result"})
	switchToOtherSession(t, m, store)
	before := len(m.conv.Messages)
	m.deliverNotice(fromBrokerMessage(queued))
	worker.Complete(nil)
	select {
	case n := <-m.mainNotices:
		t.Fatalf("old worker emitted a notice into the new session: %+v", n)
	default:
	}
	if len(m.conv.Messages) != before || len(m.pendingMessages) != 0 || m.conv.Stream.Active || len(m.services.Tracker.List()) != 0 {
		t.Fatal("old worker changed the new session's conversation or tasks")
	}
}

func TestSameOrFailedResumeKeepsTemporaryPermissions(t *testing.T) {
	m, _ := resumedModel(t, parallelResultChain())
	t.Cleanup(group.Forget)
	m.env.SessionPermissions.AllowTool("Bash")
	for _, id := range []string{"missing", m.sessionID()} {
		err := m.loadSessionByID(id)
		if (err != nil) != (id == "missing") {
			t.Fatalf("unexpected resume result: %v", err)
		}
		if !m.env.SessionPermissions.Snapshot().AllowedTools["Bash"] {
			t.Fatal("resume without an identity change discarded permission")
		}
	}
}
func TestOldOutboxCannotRestartStreamingAfterSwitch(t *testing.T) {
	m, store := resumedModel(t, parallelResultChain())
	t.Cleanup(group.Forget)
	oldOutbox := make(chan core.Event, 1)
	oldOutbox <- sdkagent.InferenceStart{}
	delayed := conv.DrainAgentOutbox(oldOutbox)()
	switchToOtherSession(t, m, store)
	before := len(m.conv.Messages)
	conv.Update(m, &m.conv, delayed)
	if m.conv.Stream.Active || len(m.conv.Messages) != before {
		t.Fatalf("retired outbox changed new session: active=%v rows=%d->%d", m.conv.Stream.Active, before, len(m.conv.Messages))
	}
}
