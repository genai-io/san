package app

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/san/tests/integration/testutil"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/group"
	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/reminder"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type failedTurnProvider struct{ restartStubProvider }

func (*failedTurnProvider) Client(string, map[string]string) (*ai.Client, error) {
	return aitest.Always(aitest.Fails(&ai.Error{Kind: ai.KindAuth, Message: "test authentication failure"})).Client(), nil
}

func resumedModel(t *testing.T, messages []core.Message) (*model, *session.Store) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := session.NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := testutil.ImportSession(store, &session.History{Metadata: session.Metadata{ID: "s"}, Messages: messages}); err != nil {
		t.Fatal(err)
	}
	opened, history, err := store.Open("s")
	if err != nil {
		t.Fatal(err)
	}
	m := &model{
		services: services{
			Agent: &agent.Runtime{}, Session: opened, SessionStore: store, LLM: &llm.Conn{},
			sessionID: opened.ID(),
			Setting:   setting.New(setting.NewData()),
			Tracker:   todo.NewStore(), Reminder: reminder.NewService(),
			Subagent: subagent.NewRegistry(),
			Task:     task.NewManager(),
			Hook:     hook.NewEngine(nil, "s", "", opened.TranscriptPath()),
		},
		conv:      conv.NewModel(80),
		autopilot: new(atomic.Pointer[autopilotRuntime]),
	}
	m.env.CWD = t.TempDir()
	m.env.SessionPermissions = setting.NewSessionPermissions()
	m.env.CurrentModel = &llm.CurrentModelInfo{ModelID: "m"}
	m.services.Agent.SetMessages(history.Messages)
	m.conv.Messages = history.ChatRows()
	t.Cleanup(func() { _ = m.services.Session.Close() })
	t.Cleanup(m.StopAgentRuntime)
	return m, store
}

func TestResumeDiscardsPreviousSessionNotices(t *testing.T) {
	m, store := resumedModel(t, parallelResultChain())
	t.Cleanup(group.Forget)
	if err := testutil.ImportSession(store, &session.History{
		Metadata: session.Metadata{ID: "other"}, Messages: []core.Message{ai.UserMessage("other conversation")},
	}); err != nil {
		t.Fatal(err)
	}
	m.services.Reminder.Enqueue("context belonging to the previous session")
	if err := m.loadSessionByID("other"); err != nil {
		t.Fatal(err)
	}
	next := m.attachPendingReminders(core.UserMessage("continue", nil))
	if strings.Contains(next.Text(), "context belonging to the previous session") {
		t.Fatal("resumed input contains context from the previous session")
	}
}

func TestSessionSwitchRetiresPreviousWorkerRequests(t *testing.T) {
	for _, action := range []string{"resume", "fork"} {
		t.Run(action, func(t *testing.T) {
			m, store := resumedModel(t, parallelResultChain())
			t.Cleanup(group.Forget)
			t.Cleanup(m.closeInteractions)
			previousUI := m.conv.AgentToUI
			question := interactionQuestion(m, context.Background(), "previous-question")
			approval := interactionApproval(m, context.Background(), "previous-approval")
			late := interactionQuestion(m, context.Background(), "late-question")
			m.enqueueInteraction(question)
			m.enqueueInteraction(approval)
			if action == "resume" {
				if err := testutil.ImportSession(store, &session.History{
					Metadata: session.Metadata{ID: "other"}, Messages: []core.Message{ai.UserMessage("other conversation")},
				}); err != nil {
					t.Fatal(err)
				}
				if err := m.loadSessionByID("other"); err != nil {
					t.Fatal(err)
				}
			} else if _, err := m.forkSession(); err != nil {
				t.Fatal(err)
			}
			if !previousUI.Closed() || m.validInteraction(late) || m.conv.Modal.Question.IsActive() {
				t.Fatal("previous session still owns the interaction UI")
			}
			select {
			case reply := <-question.Question.Reply:
				if !reply.Cancelled {
					t.Fatal("previous question was answered instead of cancelled")
				}
			default:
				t.Fatal("previous question was not cancelled")
			}
			select {
			case reply := <-approval.Permission.Response:
				if reply.Allow {
					t.Fatal("queued approval was granted during the session switch")
				}
			default:
				t.Fatal("queued approval was not cancelled")
			}
			m.enqueueInteraction(late)
			select {
			case reply := <-late.Question.Reply:
				if !reply.Cancelled {
					t.Fatal("late request from the previous session was answered")
				}
			default:
				t.Fatal("late request from the previous session was accepted")
			}
		})
	}
}

func TestSessionOpeningPreservesContextWithoutAnIdentitySwitch(t *testing.T) {
	for _, action := range []string{"same-session", "closed-session", "missing-session", "first-open"} {
		t.Run(action, func(t *testing.T) {
			m, _ := resumedModel(t, parallelResultChain())
			t.Cleanup(group.Forget)
			if action == "closed-session" {
				if err := m.services.Session.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if action == "first-open" {
				if err := m.services.Session.Close(); err != nil {
					t.Fatal(err)
				}
				m.services.Session = nil
				m.services.sessionID = "fresh"
				m.services.Agent.SetMessages(nil)
				m.conv.Messages = nil
			}
			ui := m.conv.AgentToUI
			m.services.Reminder.Enqueue("pending context for this identity")
			var err error
			switch action {
			case "same-session", "closed-session":
				err = m.loadSessionByID("s")
			case "missing-session":
				err = m.loadSessionByID("missing")
			case "first-open":
				_, err = m.openCurrentSession()
			}
			if (err != nil) != (action == "missing-session") {
				t.Fatalf("session opening error = %v, action = %s", err, action)
			}
			if m.conv.AgentToUI != ui || ui.Closed() {
				t.Fatal("opening the session retired its UI context")
			}
			if next := m.attachPendingReminders(core.UserMessage("continue", nil)); !strings.Contains(next.Text(), "pending context for this identity") {
				t.Fatal("opening the session discarded its pending context")
			}
		})
	}
}

func parallelResultChain() []core.Message {
	return []core.Message{
		{ID: "u1", Role: ai.RoleUser, Content: ai.TextContent("read both files")},
		{ID: "a1", Role: ai.RoleAssistant, Content: ai.Content{
			ai.ThinkingBlock("reasoning", "opaque-signature"),
			ai.ToolCallBlock(ai.ToolCall{ID: "c1", Name: "Read", Input: `{}`}),
			ai.ToolCallBlock(ai.ToolCall{ID: "c2", Name: "Read", Input: `{}`}),
		}},
		{ID: "r1", Role: ai.RoleUser, Content: ai.Content{
			ai.ToolResultBlock(ai.ToolResult{ToolCallID: "c1", ToolName: "Read", Content: ai.TextContent("one")}),
			ai.ToolResultBlock(ai.ToolResult{ToolCallID: "c2", ToolName: "Read", Content: ai.TextContent("two")}),
		}},
	}
}

func TestResumedParallelResultsSurviveFailureAndRetry(t *testing.T) {
	history := parallelResultChain()
	m, store := resumedModel(t, history)
	w := m.services.Session
	stopped := make(chan error, 1)
	params := agent.BuildParams{Provider: &failedTurnProvider{}, ModelID: "m", OnEvent: func(event core.Event) {
		w.RecordEvent(event)
		if event, ok := event.(core.AgentStopped); ok {
			stopped <- event.Err
		}
	}}
	if err := m.services.Agent.Start(params, m.services.Agent.Messages()); err != nil {
		t.Fatal(err)
	}
	if err := m.services.Agent.Send(withID("failed", core.UserMessage("continue", nil))); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-stopped:
		if !ai.IsKind(err, ai.KindAuth) {
			t.Fatalf("expected provider failure, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runtime did not report its failure")
	}
	m.StopAgentRuntime() // waits until execute has retired the failed run
	seed := m.services.Agent.Messages()
	if len(seed) != 4 || !reflect.DeepEqual(seed[:3], history) {
		t.Fatalf("retry lost the native parallel-result chain: %+v", seed)
	}
	provider := &restartStubProvider{requests: make(chan []core.Message, 1)}
	if err := m.services.Agent.Start(agent.BuildParams{Provider: provider, ModelID: "m", OnEvent: w.RecordEvent}, seed); err != nil {
		t.Fatal(err)
	}
	if err := m.services.Agent.Send(withID("retry", core.UserMessage("retry", nil))); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-provider.requests:
		if results := request[2].ToolResults(); len(results) != 2 {
			t.Fatalf("retry inference received %d results, want both", len(results))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retry never reached inference")
	}
	m.StopAgentRuntime()
	loaded, err := store.Read("s")
	if err != nil || len(loaded.Messages) < 5 || !reflect.DeepEqual(loaded.Messages[:3], history) {
		t.Fatalf("retry corrupted the durable history: %+v, %v", loaded, err)
	}
}

func TestCompactBeforeResumedRuntimeStartsPersistsTheNativeBoundary(t *testing.T) {
	history := parallelResultChain()
	m, store := resumedModel(t, history)
	m.HandleCompactResult(conv.CompactResultMsg{Summary: "both files read", OriginalCount: len(history)})
	seed := m.services.Agent.Messages()
	if len(seed) != 1 || seed[0].Text() != core.FormatCompactSummary("both files read") {
		t.Fatalf("inactive compaction retained old inference context: %+v", seed)
	}
	if len(m.conv.Messages) != 1 || m.conv.Messages[0].ID != seed[0].ID {
		t.Fatal("UI and native summary have different identities")
	}
	loaded, err := store.Read("s")
	if err != nil || !reflect.DeepEqual(loaded.Messages, seed) {
		t.Fatalf("compaction was not durable: %+v, %v", loaded, err)
	}
	w := m.services.Session
	provider := &restartStubProvider{requests: make(chan []core.Message, 1)}
	if err := m.services.Agent.Start(agent.BuildParams{Provider: provider, ModelID: "m", OnEvent: w.RecordEvent}, seed); err != nil {
		t.Fatal(err)
	}
	m.StopAgentRuntime()
	rows, err := store.ReadChatHistory("s")
	if err != nil || len(rows) != len(session.ChatRows(history))+1 {
		t.Fatalf("starting after compaction erased browsable history: %+v, %v", rows, err)
	}
}

func TestTextOnlyModelSwitchPreservesDurableImages(t *testing.T) {
	history := chainWithImage()
	m, store := resumedModel(t, history)
	provider := &textOnlyStubProvider{restartStubProvider{requests: make(chan []core.Message, 1)}}
	m.env.LLMProvider = provider
	w := m.services.Session
	if err := m.services.Agent.Start(agent.BuildParams{Provider: provider, ModelID: "m", OnEvent: w.RecordEvent}, m.services.Agent.Messages()); err != nil {
		t.Fatal(err)
	}
	if err := m.services.Agent.Send(withID("text", core.UserMessage("describe again", nil))); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-provider.requests:
		if imageBlocks(request[0]) != 0 {
			t.Fatal("text-only inference received an image")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("text-only turn never reached inference")
	}
	m.StopAgentRuntime()
	loaded, err := store.Read("s")
	if err != nil || len(loaded.Messages) < 3 || !reflect.DeepEqual(loaded.Messages[:2], history) {
		t.Fatalf("model switch erased durable image history: %+v, %v", loaded, err)
	}
	m.services.Agent.SetMessages(loaded.Messages)
	m.env.LLMProvider = &restartStubProvider{}
	if imageBlocks(m.services.Agent.Messages()[0]) != 1 {
		t.Fatal("vision model could not recover the historical image")
	}
}

func TestRenameKeepsTheOpenedSessionForFollowingMessages(t *testing.T) {
	m, store := resumedModel(t, []core.Message{
		{ID: "u1", Role: ai.RoleUser, Content: ai.TextContent("first prompt")},
		{ID: "a1", Role: ai.RoleAssistant, Content: ai.TextContent("first response")},
	})
	w := m.services.Session
	provider := &restartStubProvider{requests: make(chan []core.Message, 1)}
	stopped := make(chan error, 1)
	if err := m.services.Agent.Start(agent.BuildParams{Provider: provider, ModelID: "m", OnEvent: func(event core.Event) {
		w.RecordEvent(event)
		if event, ok := event.(core.AgentStopped); ok {
			stopped <- event.Err
		}
	}}, m.services.Agent.Messages()); err != nil {
		t.Fatal(err)
	}
	if err := m.renameSession("renamed"); err != nil {
		t.Fatal(err)
	}
	if !m.services.Agent.Active() || m.services.Session != w {
		t.Fatal("renaming retired the live runtime or its writer")
	}
	if err := m.services.Agent.Send(withID("after-name", core.UserMessage("continue", nil))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.requests:
	case err := <-stopped:
		t.Fatalf("runtime stopped after rename: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("turn after rename never reached inference")
	}
	m.StopAgentRuntime()
	loaded, err := store.Read("s")
	if err != nil || loaded.Metadata.Title != "renamed" || len(loaded.Messages) < 3 || loaded.Messages[2].ID != "after-name" {
		t.Fatalf("rename stopped durable message recording: %+v, %v", loaded, err)
	}
}

func TestClearAndForkReleaseThePreviousGroupIdentityImmediately(t *testing.T) {
	for _, action := range []string{"clear", "fork"} {
		t.Run(action, func(t *testing.T) {
			m, store := resumedModel(t, parallelResultChain())
			group.BindSession(m.sessionID)
			t.Cleanup(group.Forget)
			if _, err := group.Join("shop", group.Member{Name: "me", Hold: true}); err != nil {
				t.Fatal(err)
			}
			m.reconcileMembership()
			var err error
			if action == "clear" {
				err = m.clearSession()
			} else {
				_, err = m.forkSession()
			}
			if err != nil {
				t.Fatal(err)
			}
			if g, self := group.Current(); g != "" {
				t.Fatalf("new session %s retained group %s under old identity %s before polling", m.sessionID(), g, self.SessionID)
			}
			members := group.Members("shop")
			if len(members) != 1 || members[0].SessionID != "s" || members[0].Online() {
				t.Fatalf("session switch changed the source membership: %+v", members)
			}
			source, err := store.Read("s")
			if err != nil || source.Metadata.Group == "" {
				t.Fatalf("source lost its persisted membership: %+v, %v", source, err)
			}
			if action == "fork" {
				forked, err := store.Read(m.sessionID())
				if err != nil || forked.Metadata.Group != "" {
					t.Fatalf("fork inherited the source's group identity: %+v, %v", forked, err)
				}
			}
			if cmd := m.handleMemberMsg(memberMsg{status: group.Joined}); cmd != nil {
				t.Fatal("delayed group poll produced effects in the new session")
			}
		})
	}
}

func TestPendingInputSurvivesUnconfiguredProviderInNativeForm(t *testing.T) {
	m, _ := resumedModel(t, nil)
	first := withID("pending", core.UserMessage("before", []core.Attachment{{Image: ai.Image{MediaType: "image/png", Data: "aW1n"}}}))
	m.SubmitToAgent(first)
	if len(m.pendingMessages) != 1 {
		t.Fatal("unconfigured input was discarded")
	}
	m.conv.Append(core.ChatMessage{ID: "display-only", Role: core.ChatUser, Content: "must not become history"})
	added := make(chan core.Message, 2)
	provider := &restartStubProvider{requests: make(chan []core.Message, 4)}
	if err := m.services.Agent.Start(agent.BuildParams{Provider: provider, ModelID: "m", OnEvent: func(e core.Event) {
		if v, ok := e.(sdkagent.MessageAdded); ok && v.Message.Role == ai.RoleUser {
			added <- v.Message
		}
	}}, m.services.Agent.Messages()); err != nil {
		t.Fatal(err)
	}
	m.sendToAgent(pendingMessage{Message: withID("next", core.UserMessage("continue", nil))})
	for _, want := range []core.Message{first, withID("next", core.UserMessage("continue", nil))} {
		select {
		case got := <-added:
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("accepted message = %+v, want %+v", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("pending input did not reach the agent")
		}
	}
	if len(m.pendingMessages) != 0 {
		t.Fatal("accepted input remained pending")
	}
}

func TestSessionChangeClearsPreviousGoalAndPendingInput(t *testing.T) {
	for _, action := range []string{"resume", "clear"} {
		t.Run(action, func(t *testing.T) {
			m, store := resumedModel(t, nil)
			m.env.AutoPilot.Mission = "previous goal"
			m.env.AutoPilot.MissionState = setting.MissionRunning
			m.beforeGoal = new(m.env.AutoPilot)
			m.pendingMessages = []pendingMessage{{Message: core.UserMessage("old input", nil)}}
			var err error
			if action == "clear" {
				err = m.clearSession()
			} else {
				if err := testutil.ImportSession(store, &session.History{Metadata: session.Metadata{ID: "other"}}); err != nil {
					t.Fatal(err)
				}
				err = m.loadSessionByID("other")
			}
			if err != nil {
				t.Fatal(err)
			}
			if m.beforeGoal != nil || m.env.AutoPilot.Mission != "" || m.env.AutoPilot.MissionState != "" || len(m.pendingMessages) != 0 {
				t.Fatalf("session inherited previous state: %+v", m.env.AutoPilot)
			}
		})
	}
}
