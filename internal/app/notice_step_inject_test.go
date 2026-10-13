package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/todo"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
)

// runCmd executes cmd, flattening tea.Batch so the leaf commands (the ones that
// actually reach the agent) run too. Never hand it onMainNotice's result: that
// batch carries the listener re-arm, which blocks until the next notice.
func runCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, sub := range batch {
			runCmd(sub)
		}
	}
}

func TestAgentCompletionHasOneLiveAndPersistedMessage(t *testing.T) {
	store, err := session.NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rec := session.NewActive(store, "notice-1").NewWriter("main", "p", "m", 1000)
	m := &model{conv: conv.NewModel(80)}
	m.conv.OnNotice = func(msg core.ChatMessage) { rec.RecordNotice(msg.Content, msg.AgentNotice) }
	n := taskNotice()
	message := m.showNoticeLine(n)
	rec.OnAgentEvent(sdkagent.MessageAdded{Message: message})
	for _, load := range []func(string) ([]core.ChatMessage, error){store.LoadHistory, func(id string) ([]core.ChatMessage, error) {
		snapshot, err := store.Load(id)
		if err != nil {
			return nil, err
		}
		return snapshot.ChatRows(), nil
	}} {
		messages, err := load("notice-1")
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) != 1 || len(m.conv.Messages) != 1 || messages[0].Content != m.conv.Messages[0].Content {
			t.Fatalf("completion has separate display and model records: live=%+v, saved=%+v", m.conv.Messages, messages)
		}
		p := conv.RenderContext{Messages: messages, Width: 80}
		if got, want := conv.RenderSingleMessage(p, 0), conv.RenderSingleMessage(conv.RenderContext{Messages: m.conv.Messages, Width: 80}, 0); got != want {
			t.Fatalf("live and resumed completion differ: %q != %q", got, want)
		}
	}
}

func TestAgentNoticeKeepsForegroundResultPaired(t *testing.T) {
	m := commitTestModel(core.ChatMessage{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{
		{ID: "foreground", Name: "Agent", Input: `{"prompt":"Review"}`},
	}})
	m.showNoticeLine(taskNotice())
	m.conv.Append(core.ChatMessage{Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: "foreground", ToolName: "Agent"}})
	if !m.messageRenderParams().InlinedResults.IsResultInlined(2) {
		t.Fatal("background notice detached the foreground result")
	}
	// Real user input still ends the batch's result scan.
	m.conv.Messages[1].Content = "review a different file"
	if m.messageRenderParams().InlinedResults.IsResultInlined(2) {
		t.Fatal("result pairing crossed real user input")
	}
}

// noticeDeliveryModel returns a model whose agent session is live, plus the
// provider that records the message chain of its next inference — which is how
// these tests see what the model actually got to read.
func noticeDeliveryModel(t *testing.T) (*model, *restartStubProvider) {
	t.Helper()

	provider := &restartStubProvider{requests: make(chan []core.Message, 2)}
	sess := &agent.Runtime{}
	if err := sess.Start(agent.BuildParams{Provider: provider, ModelID: "m"}, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(sess.Stop)

	return &model{
		services: services{
			Agent:    sess,
			Tracker:  todo.NewStore(),
			Subagent: subagent.NewRegistry(),
			Reminder: reminder.NewService(),
		},
		conv: conv.NewModel(80),
	}, provider
}

func taskNotice() mainNotice {
	return mainNotice{
		Display:   "Backend: research completed",
		Content:   `<task-notification task-id="t1" status="completed" description="Backend: research">done</task-notification>`,
		FromAgent: true,
	}
}

// assertNoticeShown fails unless the notice's display line is in the
// conversation.
func assertNoticeShown(t *testing.T, m *model) {
	t.Helper()
	for i := range m.conv.Messages {
		if strings.Contains(conv.RenderSingleMessage(conv.RenderContext{Messages: m.conv.Messages, Width: 80}, i), "research completed") {
			return
		}
	}
	t.Fatalf("notice line was not shown in the conversation: %+v", m.conv.Messages)
}

// assertAgentRead fails unless the agent's next inference carried the task
// notification.
func assertAgentRead(t *testing.T, provider *restartStubProvider) {
	t.Helper()
	select {
	case chain := <-provider.requests:
		for _, msg := range chain {
			if strings.Contains(msg.Text(), "task-notification") {
				return
			}
		}
		t.Fatalf("agent inference carried no task notification: %+v", chain)
	case <-time.After(2 * time.Second):
		t.Fatal("notice never reached the agent")
	}
}

// A task finishing while a tool runs is shown on arrival — the tail is a
// tool-result row then — and handed to the running turn.
func TestNoticeDuringToolExecutionIsShownAndDelivered(t *testing.T) {
	m, provider := noticeDeliveryModel(t)
	m.conv.Stream.Active = true
	m.conv.Append(core.ChatMessage{Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: "c1"}})

	runCmd(m.deliverNotice(taskNotice()))

	assertNoticeShown(t, m)
	assertAgentRead(t, provider)
}

func TestNoticeWhileStreamingIsShownAndDelivered(t *testing.T) {
	m, provider := noticeDeliveryModel(t)
	m.conv.Stream.Active = true
	owner := m.conv.Append(core.ChatMessage{Role: core.ChatAssistant, Content: "thinking out loud"})
	m.conv.Stream.MessageID = owner.ID

	runCmd(m.deliverNotice(taskNotice()))
	assertNoticeShown(t, m)
	assertAgentRead(t, provider)

	// The tool batch completes: its result lands, leaving an appendable tail.
	m.conv.Append(core.ChatMessage{Role: core.ChatUser, ToolResult: &core.ToolResult{ToolCallID: "c1"}})
	runCmd(m.OnStepEnd())

	assertNoticeShown(t, m)
}

func TestNoticeIsVisibleBeforeInferenceEnds(t *testing.T) {
	m, _ := noticeDeliveryModel(t)
	m.env.Width = 80
	m.conv.Stream.Active = true
	owner := m.conv.Append(core.ChatMessage{Role: core.ChatAssistant})
	m.conv.Stream.MessageID = owner.ID
	m.deliverNotice(taskNotice())

	view := conv.RenderActiveContent(m.messageRenderParams())
	if !strings.Contains(view, "research completed") || !strings.Contains(view, "Thinking") {
		t.Fatalf("completion must be visible while inference is still running:\n%s", view)
	}
	if m.conv.CommittedCount != 0 {
		t.Fatal("notice committed the unfinished inference")
	}
	m.conv.AppendStream("continuing", "")
	if got := m.conv.Messages[0].Content; got != "continuing" {
		t.Fatalf("notice displaced subsequent stream output: %q", got)
	}
}

// With nothing parked and nothing queued, a step boundary must not disturb the
// running turn: no command at all.
func TestStepDrainWithNothingPendingIsInert(t *testing.T) {
	m, _ := noticeDeliveryModel(t)
	m.conv.Stream.Active = true

	if cmd := m.OnStepEnd(); cmd != nil {
		t.Fatal("step drain produced work with nothing to release")
	}
	if len(m.conv.Messages) != 0 {
		t.Fatalf("step drain added messages with nothing to release: %+v", m.conv.Messages)
	}
}
