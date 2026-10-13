package app

import (
	"reflect"
	"testing"
	"time"

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
	if err := store.Save(&session.Snapshot{Metadata: session.Metadata{ID: "s"}, Messages: messages}); err != nil {
		t.Fatal(err)
	}
	active := session.NewActive(store, "s")
	snapshot, err := active.Resume("s")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = active.Close() })
	m := &model{
		services: services{
			Agent: &agent.Runtime{}, Session: active, LLM: &llm.Conn{},
			Tracker: todo.NewStore(), Reminder: reminder.NewService(),
			Subagent: subagent.NewRegistry(),
			Task:     task.NewManager(),
			Hook:     hook.NewEngine(nil, "s", "", active.TranscriptPath()),
		},
		conv: conv.NewModel(80),
	}
	m.env.CWD = t.TempDir()
	m.env.SessionPermissions = setting.NewSessionPermissions()
	m.env.CurrentModel = &llm.CurrentModelInfo{ModelID: "m"}
	m.services.Agent.SetMessages(snapshot.Messages)
	m.conv.Messages = snapshot.ChatRows()
	t.Cleanup(m.StopAgentRuntime)
	return m, store
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
	w := m.services.Session.NewWriter("main", "test", "m", 100)
	stopped := make(chan error, 1)
	params := agent.BuildParams{Provider: &failedTurnProvider{}, ModelID: "m", OnEvent: func(event core.Event) {
		w.OnAgentEvent(event)
		if event, ok := event.(core.AgentStopped); ok {
			stopped <- event.Err
		}
	}}
	if err := m.services.Agent.Start(params, m.seedAgentMessages("")); err != nil {
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
	seed := m.seedAgentMessages("")
	if len(seed) != 4 || !reflect.DeepEqual(seed[:3], history) {
		t.Fatalf("retry lost the native parallel-result chain: %+v", seed)
	}
	provider := &restartStubProvider{requests: make(chan []core.Message, 1)}
	if err := m.services.Agent.Start(agent.BuildParams{Provider: provider, ModelID: "m", OnEvent: w.OnAgentEvent}, seed); err != nil {
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
	loaded, err := store.Load("s")
	if err != nil || len(loaded.Messages) < 5 || !reflect.DeepEqual(loaded.Messages[:3], history) {
		t.Fatalf("retry corrupted the durable history: %+v, %v", loaded, err)
	}
}

func TestCompactBeforeResumedRuntimeStartsPersistsTheNativeBoundary(t *testing.T) {
	history := parallelResultChain()
	m, store := resumedModel(t, history)
	m.HandleCompactResult(conv.CompactResultMsg{Summary: "both files read", OriginalCount: len(history)})
	seed := m.seedAgentMessages("")
	if len(seed) != 1 || seed[0].Text() != core.FormatCompactSummary("both files read") {
		t.Fatalf("inactive compaction retained old inference context: %+v", seed)
	}
	if len(m.conv.Messages) != 1 || m.conv.Messages[0].ID != seed[0].ID {
		t.Fatal("UI and native summary have different identities")
	}
	loaded, err := store.Load("s")
	if err != nil || !reflect.DeepEqual(loaded.Messages, seed) {
		t.Fatalf("compaction was not durable: %+v, %v", loaded, err)
	}
	w := m.services.Session.Writer()
	provider := &restartStubProvider{requests: make(chan []core.Message, 1)}
	if err := m.services.Agent.Start(agent.BuildParams{Provider: provider, ModelID: "m", OnEvent: w.OnAgentEvent}, seed); err != nil {
		t.Fatal(err)
	}
	m.StopAgentRuntime()
	rows, err := store.LoadHistory("s")
	if err != nil || len(rows) != len(session.ChatRows(history))+1 {
		t.Fatalf("starting after compaction erased browsable history: %+v, %v", rows, err)
	}
}

func TestTextOnlyModelSwitchPreservesDurableImages(t *testing.T) {
	history := chainWithImage()
	m, store := resumedModel(t, history)
	provider := &textOnlyStubProvider{restartStubProvider{requests: make(chan []core.Message, 1)}}
	m.env.LLMProvider = provider
	w := m.services.Session.NewWriter("main", provider.Name(), "m", 100)
	if err := m.services.Agent.Start(agent.BuildParams{Provider: provider, ModelID: "m", OnEvent: w.OnAgentEvent}, m.seedAgentMessages("")); err != nil {
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
	loaded, err := store.Load("s")
	if err != nil || len(loaded.Messages) < 3 || !reflect.DeepEqual(loaded.Messages[:2], history) {
		t.Fatalf("model switch erased durable image history: %+v, %v", loaded, err)
	}
	m.services.Agent.SetMessages(loaded.Messages)
	m.env.LLMProvider = &restartStubProvider{}
	if imageBlocks(m.seedAgentMessages("")[0]) != 1 {
		t.Fatal("vision model could not recover the historical image")
	}
}

func TestRenameKeepsTheLiveWriterForFollowingMessages(t *testing.T) {
	m, store := resumedModel(t, []core.Message{
		{ID: "u1", Role: ai.RoleUser, Content: ai.TextContent("first prompt")},
		{ID: "a1", Role: ai.RoleAssistant, Content: ai.TextContent("first response")},
	})
	w := m.services.Session.Writer()
	provider := &restartStubProvider{requests: make(chan []core.Message, 1)}
	stopped := make(chan error, 1)
	if err := m.services.Agent.Start(agent.BuildParams{Provider: provider, ModelID: "m", OnEvent: func(event core.Event) {
		w.OnAgentEvent(event)
		if event, ok := event.(core.AgentStopped); ok {
			stopped <- event.Err
		}
	}}, m.seedAgentMessages("")); err != nil {
		t.Fatal(err)
	}
	if err := m.renameSession("renamed"); err != nil {
		t.Fatal(err)
	}
	if !m.services.Agent.Active() || m.services.Session.Writer() != w {
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
	loaded, err := store.Load("s")
	if err != nil || loaded.Metadata.Title != "renamed" || len(loaded.Messages) < 3 || loaded.Messages[2].ID != "after-name" {
		t.Fatalf("rename stopped durable message recording: %+v, %v", loaded, err)
	}
}

func TestClearAndForkReleaseThePreviousGroupIdentityImmediately(t *testing.T) {
	for _, action := range []string{"clear", "fork"} {
		t.Run(action, func(t *testing.T) {
			m, store := resumedModel(t, parallelResultChain())
			group.BindSession(m.services.Session.ID)
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
				t.Fatalf("new session %s retained group %s under old identity %s before polling", m.services.Session.ID(), g, self.SessionID)
			}
			members := group.Members("shop")
			if len(members) != 1 || members[0].SessionID != "s" || members[0].Online() {
				t.Fatalf("session switch changed the source membership: %+v", members)
			}
			source, err := store.Load("s")
			if err != nil || source.Metadata.Group == "" {
				t.Fatalf("source lost its persisted membership: %+v, %v", source, err)
			}
			if action == "fork" {
				forked, err := store.Load(m.services.Session.ID())
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
