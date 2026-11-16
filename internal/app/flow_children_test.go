package app

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestManagedAgentViewsFollowSessionAndCompletion(t *testing.T) {
	m := fixedComposerModel(80, 24)
	m.env.AgentPreviewLines = 3
	m.services.Task = task.NewManager()
	task.SetLifecycleHandler(nil)
	own := m.services.Task.CreateChildAgentTask("own", "scheduler", "reviewer", "Security review", task.WithSessionID(context.Background(), m.sessionID()), nil)
	own.SetIdentity("reviewer", "child-session")
	own.AppendProgress("Read(boundary.go)")
	foreign := m.services.Task.CreateChildAgentTask("foreign", "other-scheduler", "reviewer", "Foreign review", task.WithSessionID(context.Background(), "other-session"), nil)
	ordinary := m.services.Task.CreateAgentTask("ordinary", "reviewer", "Ordinary review", context.Background(), nil)
	t.Cleanup(func() { own.Complete(nil); foreign.Complete(nil); ordinary.Complete(nil) })
	p := m.messageRenderParams()
	if len(p.ManagedAgents) != 1 || p.ManagedAgents[0].ID != "own" || p.ManagedAgents[0].SessionID != "child-session" {
		t.Fatalf("wrong managed worker projection: %+v", p.ManagedAgents)
	}
	if got := ansi.Strip(conv.RenderActiveBody(p)); !strings.Contains(got, "reviewer: Security review") || !strings.Contains(got, "Read(boundary.go)") || strings.Contains(got, "Foreign review") {
		t.Fatalf("wrong native activity: %s", got)
	}
	if len(m.conv.Messages) != 0 || len(m.conv.Tool.PendingCalls) != 0 || m.conv.Stream.Active {
		t.Fatal("worker presentation fabricated conversation or model activity")
	}
	own.Complete(nil)
	if pending := m.messageRenderParams().ManagedAgents; len(pending) != 1 || pending[0].Status != "completed" {
		t.Fatalf("worker disappeared before its completion notice: %+v", pending)
	}
	m.taskCompletionsShown = map[string]bool{taskCompletionKey("own", own.GetStatus().Attempt): true}
	if shown := m.messageRenderParams().ManagedAgents; len(shown) != 0 {
		t.Fatalf("delivered completion left a duplicate live worker: %+v", shown)
	}
}

func TestFlowChildrenNativeVisibilityAndDisplayOnlyCompletion(t *testing.T) {
	m, mainProvider := noticeDeliveryModel(t)
	attachTurnSession(t, m)
	m.services.Task = task.NewManager()
	m.services.Task.SetSessionID(m.sessionID())
	m.mainNotices = make(chan mainNotice, 8)
	m.env.Width, m.env.AgentPreviewLines = 80, 3
	m.conv.OnNotice = m.recordNotice
	m.wireTaskLifecycle(nil)
	t.Cleanup(func() { task.SetLifecycleHandler(nil) })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	started := make(chan string, 4)
	gates := map[string]chan struct{}{}
	for _, name := range []string{"map", "security", "performance", "report"} {
		gates[name] = make(chan struct{})
	}
	childProvider := &compactHTTPProvider{client: aitest.Always(func(callCtx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		text := req.Messages[len(req.Messages)-1].Text()
		name := ""
		for _, candidate := range []string{"report", "performance", "security", "map"} {
			if strings.HasPrefix(text, "MARK_"+candidate) {
				name = candidate
				break
			}
		}
		if name == "" {
			return aitest.Fails(fmt.Errorf("unrecognized worker prompt: %s", text))(callCtx, req)
		}
		started <- name
		select {
		case <-gates[name]:
		case <-ctx.Done():
			return aitest.Fails(ctx.Err())(callCtx, req)
		case <-callCtx.Done():
			return aitest.Fails(callCtx.Err())(callCtx, req)
		}
		return aitest.Says("fourth hidden detail\n"+strings.Repeat("supporting detail ", 40)+"\n"+name+" actual answer")(callCtx, req)
	}).Client()}
	executor := subagent.NewExecutor(childProvider, m.env.CWD, "m", nil)
	if err := executor.SetSessions(m.services.SessionStore, m.services.Session); err != nil {
		t.Fatal(err)
	}
	executor.ConfigureOwner(m.services.Task, nil, nil, nil)
	svc := flow.NewService(m.services.Task)
	svc.SetSearchPaths([]string{t.TempDir()})
	svc.SetExecutor(subagent.NewExecutorAdapter(executor))
	m.services.Flow = svc
	definition := "```mermaid\nflowchart LR\n map --> security\n map --> performance\n security --> report\n performance --> report\n```\n\n## map\nMARK_map\n\n## security\nMARK_security\n\n## performance\nMARK_performance\n\n## report\nMARK_report\n"
	if err := svc.SaveDefinition("native-children", definition, ""); err != nil {
		t.Fatal(err)
	}
	final := make(chan broker.Message, 2)
	broker.Register(broker.Main, func(msg broker.Message) bool { final <- msg; return true })
	t.Cleanup(func() { broker.Unregister(broker.Main) })
	_, parentID, err := svc.Launch("native-children", nil, 80)
	if err != nil {
		t.Fatal(err)
	}
	parent, ok := m.services.Task.Get(parentID)
	if !ok {
		t.Fatal("missing native scheduler")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-final:
		case <-time.After(5 * time.Second):
		}
	})
	awaitStart := func() string {
		t.Helper()
		select {
		case name := <-started:
			return name
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not reach provider")
			return ""
		}
	}
	assertLive := func(names ...string) {
		t.Helper()
		graph := parent.GetStatus().LiveView
		if !strings.Contains(graph, "map") || !strings.Contains(graph, "report") {
			t.Fatalf("missing graph: %s", graph)
		}
		m.workerMu.Lock()
		view := ansi.Strip(m.renderTrackerList())
		m.workerMu.Unlock()
		if !strings.Contains(view, "Native children") {
			t.Fatalf("scheduler graph missing from task area: %s", view)
		}
		if strings.Contains(view, "Tasks") || strings.Contains(view, "native-children/") {
			t.Fatalf("managed children duplicated in footer: %s", view)
		}
		params := m.messageRenderParams()
		live := ansi.Strip(conv.RenderActiveBody(params))
		for _, name := range names {
			if !strings.Contains(live, "Agent: "+name) || !strings.Contains(live, "Thinking...") {
				t.Fatalf("native worker %s missing from conversation: %s", name, live)
			}
			found := false
			for _, bg := range m.services.Task.ListRunning() {
				info := bg.GetStatus()
				if info.ParentTaskID != parentID || !strings.Contains(info.Description, name) {
					continue
				}
				if info.ParentSessionID != m.sessionID() {
					t.Fatalf("wrong child session: %+v", info)
				}
				m.workerMu.Lock()
				tracked := false
				for _, item := range m.services.Tracker.List() {
					if todo.BackgroundTaskID(item) == info.ID {
						tracked = true
					}
				}
				m.workerMu.Unlock()
				if !tracked {
					t.Fatalf("native child %s absent from worker tracker", info.ID)
				}
				projected := false
				for _, agent := range params.ManagedAgents {
					if agent.ID == info.ID && agent.SessionID == info.AgentSessionID && agent.Description == name {
						projected = true
					}
				}
				if !projected {
					t.Fatalf("native child identity missing from conversation: %+v", info)
				}
				found = true
			}
			if !found {
				t.Fatalf("no running native child for %s", name)
			}
		}
	}
	deliverChild := func() {
		t.Helper()
		select {
		case n := <-m.mainNotices:
			if n.Content != "" || !n.FromAgent || n.FinishedTaskID == "" || n.SessionID != m.sessionID() || !strings.Contains(n.Display, "actual answer") {
				t.Fatalf("not a display-only native answer: %+v", n)
			}
			runCmd(m.deliverNotice(n))
			msg := m.conv.Messages[len(m.conv.Messages)-1]
			if msg.Role != core.ChatNotice || !msg.AgentNotice || !strings.Contains(msg.Content, "actual answer") {
				t.Fatalf("child result not shown as native notice: %+v", msg)
			}
			rendered := ansi.Strip(conv.RenderSingleMessage(m.messageRenderParams(), len(m.conv.Messages)-1))
			if !strings.Contains(rendered, "actual answer") || strings.Contains(rendered, "fourth hidden detail") || !strings.Contains(rendered, "Agent:") {
				t.Fatalf("wrong native bounded answer preview: %s", rendered)
			}
			for _, agent := range m.messageRenderParams().ManagedAgents {
				if agent.ID == n.FinishedTaskID {
					t.Fatalf("shown result retained a duplicate live block: %+v", agent)
				}
			}
			select {
			case req := <-mainProvider.requests:
				t.Fatalf("managed child woke main model: %+v", req)
			default:
			}
		case <-time.After(5 * time.Second):
			t.Fatal("missing child completion")
		}
	}
	if got := awaitStart(); got != "map" {
		t.Fatalf("first worker=%s", got)
	}
	assertLive("map")
	close(gates["map"])
	a, b := awaitStart(), awaitStart()
	if a == b || (a != "security" && a != "performance") || (b != "security" && b != "performance") {
		t.Fatalf("parallel workers=%s,%s", a, b)
	}
	assertLive("security", "performance")
	deliverChild()
	close(gates["security"])
	close(gates["performance"])
	if got := awaitStart(); got != "report" {
		t.Fatalf("join worker=%s", got)
	}
	deliverChild()
	deliverChild()
	close(gates["report"])
	deliverChild()
	select {
	case msg := <-final:
		if msg.From != parentID {
			t.Fatalf("unexpected scheduler completion: %+v", msg)
		}
		final <- msg
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not complete")
	}
	record, err := svc.InspectRun(parentID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != flow.RunSucceeded || len(record.Nodes) != 4 {
		t.Fatalf("wrong final checkpoint: %+v", record)
	}
	for name, checkpoint := range record.Nodes {
		identity := checkpoint.Execution
		bg, ok := m.services.Task.Get(identity.TaskID)
		if !ok || identity.SessionID == "" {
			t.Fatalf("%s has no native execution: %+v", name, checkpoint)
		}
		info := bg.GetStatus()
		if info.ParentTaskID != parentID || info.ID != identity.TaskID || info.AgentSessionID != identity.SessionID {
			t.Fatalf("checkpoint identity diverged: %+v %+v", identity, info)
		}
		child, err := m.services.SessionStore.Read(t.Context(), identity.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		if len(child.Messages()) == 0 {
			t.Fatalf("%s child session has no actual conversation", name)
		}
	}
	snapshot := m.services.Session.Snapshot()
	select {
	case req := <-mainProvider.requests:
		t.Fatalf("managed flow children woke main model: %+v", req)
	default:
	}
	if got := len(snapshot.Messages()); got != 0 {
		t.Fatalf("display-only results added %d model messages", got)
	}
	saved, err := m.services.SessionStore.Read(t.Context(), m.sessionID())
	if err != nil {
		t.Fatal(err)
	}
	restored := m.services.SessionStore.ChatRows(saved)
	if len(restored) != 4 {
		t.Fatalf("persisted child annotations=%d", len(restored))
	}
	for i, row := range restored {
		if row.Role != core.ChatNotice || !row.AgentNotice || row.Content != m.conv.Messages[i].Content {
			t.Fatalf("restored native result differs: %+v", row)
		}
		got := conv.RenderSingleMessage(conv.RenderContext{Messages: restored, Width: 80, AgentPreviewLines: 3}, i)
		want := conv.RenderSingleMessage(m.messageRenderParams(), i)
		if got != want {
			t.Fatalf("restored rendering differs:\n%s\n%s", got, want)
		}
	}
}
