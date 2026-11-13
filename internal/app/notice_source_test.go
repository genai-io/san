package app

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/group"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/tests/integration/testutil"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestBrokerNoticeRetainsOriginalReceivingHandle(t *testing.T) {
	m, store := resumedModel(t, nil)
	t.Cleanup(group.Forget)
	t.Cleanup(func() { broker.Unregister(broker.Main) })
	original := m.services.Session
	m.mainNotices = make(chan mainNotice, 1)
	m.bindMainBroker(original)
	source := session.EventRef{Session: "child", Seq: 7}
	if !broker.Send(broker.Message{To: broker.Main, SessionID: original.ID(), SourceSession: source.Session, SourceSeq: source.Seq, Content: "queued child report"}) {
		t.Fatal("notice was not staged")
	}
	notice := <-m.mainNotices
	if notice.Destination != original || !reflect.DeepEqual(notice.Source, &source) {
		t.Fatalf("staging lost original handles or source: %+v", notice)
	}
	if err := original.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := store.Open(t.Context(), original.ID())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.replaceSession(replacement); err != nil {
		t.Fatal(err)
	}
	before := len(m.conv.Messages)
	if cmd := m.deliverNotice(notice); cmd != nil || len(m.conv.Messages) != before || m.pendingMessages.Len() != 0 {
		t.Fatal("queued notice was redirected through a same-ID replacement handle")
	}
}

func TestChildNoticeNativeAcceptancePreservesSource(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted", true: "invalid-source"}[invalid], func(t *testing.T) {
			m, store := resumedModel(t, nil)
			t.Cleanup(group.Forget)
			owner := m.services.Session
			child := mustCreateSession(t, store, session.CreateOptions{ID: "notice-child", Parent: &session.ParentLink{Session: owner.ID()}})
			finding := ai.Message{ID: "child-finding", Role: ai.RoleAssistant, Content: ai.TextContent("real child finding")}
			if err := testutil.AppendMessages(child, []ai.Message{finding}); err != nil {
				t.Fatal(err)
			}
			var source session.EventRef
			childProjection := child.Snapshot()
			for _, event := range childProjection.Events() {
				if payload, ok := event.Payload.(*session.MessageAppendPayload); ok && payload.Messages[0].ID == finding.ID {
					source = session.EventRef{Session: child.ID(), Seq: event.Seq}
				}
			}
			if source.Seq == 0 {
				t.Fatal("child result has no native evidence")
			}
			if invalid {
				source.Seq += 1000
			}
			provider := &restartStubProvider{requests: make(chan []core.Message, 1)}
			if err := m.services.Agent.Start(agent.BuildParams{Provider: provider, ModelID: "m", CWD: m.env.CWD, Recording: owner, PermissionRules: (agent.PermissionPolicy{}).Decide}, nil); err != nil {
				t.Fatal(err)
			}
			m.conv.Stream.Active = true
			m.services.Reminder.Enqueue("prepared receiver reminder")
			notice := fromBrokerMessage(broker.Message{SourceSession: source.Session, SourceSeq: source.Seq, Content: "<agent-message>real child finding</agent-message>"})
			notice.Destination = owner
			m.deliverNotice(notice)
			if m.pendingMessages.Len() != 0 {
				t.Fatal("accepted or rejected foreign mail remained eligible for retry")
			}
			if invalid {
				stopRuntime(t, m.services.Agent)
				projection := owner.Snapshot()
				if len(provider.requests) != 0 || len(projection.Messages()) != 0 {
					t.Fatal("invalid child evidence reached native inference or history")
				}
				return
			}
			select {
			case request := <-provider.requests:
				if len(request) != 1 || !strings.Contains(request[0].Text(), notice.Content) || !strings.Contains(request[0].Text(), "prepared receiver reminder") {
					t.Fatalf("native request lost prepared child input: %+v", request)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("child notice never reached native inference")
			}
			stopRuntime(t, m.services.Agent)
			found := false
			projection := owner.Snapshot()
			for _, event := range projection.Events() {
				if payload, ok := event.Payload.(*session.MessageAppendPayload); ok && reflect.DeepEqual(event.Source, &source) {
					found = len(payload.Messages) == 1 && strings.Contains(payload.Messages[0].Text(), "prepared receiver reminder")
				}
			}
			if !found {
				t.Fatal("prepared notice and actual child source were not committed together")
			}
		})
	}
}

func TestTaskCompletionCarriesOnlyActualChildEvidence(t *testing.T) {
	for _, seq := range []uint64{0, 19} {
		message, ok := taskCompletionMessage(task.TaskInfo{ID: "worker", Type: task.TaskTypeAgent, Status: task.StatusCompleted, AgentSessionID: "actual-child", SourceMessageSeq: seq})
		if !ok {
			t.Fatal("completed task omitted its notice")
		}
		if message.SourceSeq != seq || (seq == 0 && message.SourceSession != "") || (seq != 0 && message.SourceSession != "actual-child") {
			t.Fatalf("task evidence was invented or lost: %+v", message)
		}
	}
}
