package tui

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/tui/chat"
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
	m.features.Task = task.NewRunSet()
	task.SetLifecycleHandler(nil)
	own := m.features.Task.CreateChildAgentTask("own", "scheduler", "reviewer", "Security review", task.WithSessionID(context.Background(), m.sessionID()), nil)
	own.SetIdentity("reviewer", "child-session")
	own.AppendProgress("Read(boundary.go)")
	foreign := m.features.Task.CreateChildAgentTask("foreign", "other-scheduler", "reviewer", "Foreign review", task.WithSessionID(context.Background(), "other-session"), nil)
	ordinary := m.features.Task.CreateAgentTask("ordinary", "reviewer", "Ordinary review", context.Background(), nil)
	t.Cleanup(func() { own.Complete(nil); foreign.Complete(nil); ordinary.Complete(nil) })
	p := m.messageRenderParams()
	if len(p.ManagedAgents) != 1 || p.ManagedAgents[0].ID != "own" || p.ManagedAgents[0].SessionID != "child-session" {
		t.Fatalf("wrong managed worker projection: %+v", p.ManagedAgents)
	}
	if got := ansi.Strip(chat.RenderActiveBody(p)); !strings.Contains(got, "reviewer: Security review") || !strings.Contains(got, "Read(boundary.go)") || strings.Contains(got, "Foreign review") {
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

func TestFlowSchedulerTerminalReceiptsDoNotEnterModel(t *testing.T) {
	for _, status := range []string{"failed", "stopped", "completed"} {
		for _, busy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/busy=%t", status, busy), func(t *testing.T) {
				m, provider := noticeDeliveryModel(t)
				attachTurnSession(t, m)
				m.features.Task = task.NewRunSet()
				m.features.Task.SetSessionID(m.sessionID())
				m.mainNotices = make(chan mainNotice, 8)
				m.conv.OnNotice = m.recordNotice
				m.wireTaskLifecycle(nil)
				t.Cleanup(func() { task.SetLifecycleHandler(nil) })
				m.conv.Stream.Active = busy
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				parent := m.features.Task.CreateSchedulerTask("scheduler", "Security audit", "raw-launch-name", ctx, cancel)
				select {
				case n := <-m.mainNotices:
					if n.Content != "" || n.FinishedTaskID != "" || !strings.Contains(n.Display, `status="running"`) {
						t.Fatalf("invalid scheduler start: %+v", n)
					}
					runCmd(m.deliverNotice(n))
				case <-time.After(time.Second):
					t.Fatal("missing scheduler start")
				}
				if status != "completed" {
					parent.SetResult("UNSUCCESSFUL RESULT MUST NOT BE SHOWN")
				}
				if status == "failed" {
					parent.Complete(fmt.Errorf("checkpoint write denied"))
				} else if status == "completed" {
					parent.Complete(nil)
				} else {
					if err := parent.Stop(); err != nil {
						t.Fatal(err)
					}
					parent.Complete(context.Canceled)
				}
				select {
				case n := <-m.mainNotices:
					if n.Content != "" || !n.FromAgent || n.FinishedTaskID != parent.GetID() || n.SessionID != m.sessionID() ||
						!strings.Contains(n.Display, `scheduler="true"`) || !strings.Contains(n.Display, `status="`+status+`"`) ||
						!strings.Contains(n.Display, `agent-name="Security audit"`) {
						t.Fatalf("invalid terminal scheduler receipt: %+v", n)
					}
					if status == "failed" && !strings.Contains(n.Display, "checkpoint write denied") {
						t.Fatalf("terminal diagnostic lost: %+v", n)
					}
					if status != "completed" && strings.Contains(n.Display, `status="completed"`) {
						t.Fatalf("unsuccessful scheduler presented success: %+v", n)
					}
					runCmd(m.deliverNotice(n))
					rendered := ansi.Strip(chat.RenderSingleMessage(m.messageRenderParams(), len(m.conv.Messages)-1))
					if status != "completed" && (strings.Contains(rendered, "Flow result") || strings.Contains(rendered, "UNSUCCESSFUL RESULT")) {
						t.Fatalf("unsuccessful scheduler displayed a success result: %s", rendered)
					}
					if status == "completed" {
						if !strings.Contains(rendered, "No final result") {
							t.Fatalf("empty scheduler answer invented or hid a conclusion: %s", rendered)
						}
					}
				case <-time.After(time.Second):
					t.Fatal("missing terminal scheduler receipt")
				}
				snapshot := m.features.Session.Snapshot()
				if len(m.conv.Messages) != 2 || len(snapshot.Messages()) != 0 {
					t.Fatalf("scheduler receipts entered model history or were hidden: %+v", m.conv.Messages)
				}
				select {
				case req := <-provider.requests:
					t.Fatalf("scheduler woke main model: %+v", req)
				default:
				}

				// A late completion from a different session must not leak into
				// either the visible transcript or the current model's queue.
				foreign := m.features.Task.CreateSchedulerTask("foreign", "Foreign audit", "foreign", task.WithSessionID(context.Background(), "other-session"), nil)
				foreign.Complete(fmt.Errorf("foreign diagnostic"))
				select {
				case n := <-m.mainNotices:
					t.Fatalf("foreign scheduler crossed session fence: %+v", n)
				default:
				}
			})
		}
	}
}

func TestFlowChildrenNativeVisibilityAndDisplayOnlyCompletion(t *testing.T) {
	m, mainProvider := noticeDeliveryModel(t)
	attachTurnSession(t, m)
	m.features.Task = task.NewRunSet()
	m.features.Task.SetSessionID(m.sessionID())
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
	const conclusion = "Final conclusion: boundary checks need repair"
	const taskOutputLimit = 512 * 1024 // Native task output_buffer.go cap; Result must not share it.
	var report strings.Builder
	report.WriteString("## " + conclusion + "\n\n")
	for i := 1; i <= 7000; i++ {
		fmt.Fprintf(&report, "Report detail %04d: supporting evidence for the full independent Markdown report, not a preview.\n\n", i)
	}
	const reportTail = "TAIL DISCLAIMER: report actual answer is not a substitute for review."
	report.WriteString(reportTail)
	reportOutput := report.String()
	if len(reportOutput) <= taskOutputLimit || len(reportOutput) <= maxTaskOutputInNotification {
		t.Fatal("fixture must exceed both task output and notification preview limits")
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
		if name == "report" {
			return aitest.Says(reportOutput)(callCtx, req)
		}
		return aitest.Says("fourth hidden detail\n"+strings.Repeat("supporting detail ", 40)+"\n"+name+" actual answer")(callCtx, req)
	}).Client()}
	executor := subagent.NewExecutor(childProvider, m.env.CWD, "m", nil)
	if err := executor.SetSessions(m.features.SessionStore, m.features.Session); err != nil {
		t.Fatal(err)
	}
	executor.ConfigureOwner(m.features.Task, nil, nil, nil)
	svc := flow.NewScheduler(m.features.Task)
	svc.SetSearchPaths([]string{t.TempDir()})
	svc.SetExecutor(subagent.NewExecutorAdapter(executor))
	m.features.Flow = svc
	definition := "```mermaid\nflowchart LR\n map --> security\n map --> performance\n security --> report\n performance --> report\n```\n\n## map\nMARK_map\n\n## security\nMARK_security\n\n## performance\nMARK_performance\n\n## report\nMARK_report\n"
	if err := svc.SaveDefinition("native-children", definition, ""); err != nil {
		t.Fatal(err)
	}
	parentID, err := svc.Launch("native-children", nil, 80)
	if err != nil {
		t.Fatal(err)
	}
	parent, ok := m.features.Task.Get(parentID)
	if !ok {
		t.Fatal("missing native scheduler")
	}
	if info := parent.GetStatus(); !info.Scheduler || info.AgentName != "Native children" || info.Description != "/flow run native-children" {
		t.Fatalf("scheduler identity lost human title or raw launch description: %+v", info)
	}
	t.Cleanup(func() {
		cancel()
		deadline := time.After(5 * time.Second)
		for parent.GetStatus().Status == task.StatusRunning {
			select {
			case <-deadline:
				t.Error("scheduler did not stop during cleanup")
				return
			case <-time.After(time.Millisecond):
			}
		}
	})
	deliverScheduler := func(status string) {
		t.Helper()
		select {
		case n := <-m.mainNotices:
			if n.Content != "" || !n.FromAgent || n.SessionID != m.sessionID() ||
				!strings.Contains(n.Display, `scheduler="true"`) ||
				!strings.Contains(n.Display, `agent-name="Native children"`) ||
				!strings.Contains(n.Display, `status="`+status+`"`) {
				t.Fatalf("not an attributed display-only scheduler receipt: %+v", n)
			}
			if status == "running" && n.FinishedTaskID != "" {
				t.Fatalf("start marked scheduler complete: %+v", n)
			}
			if status != "running" && n.FinishedTaskID != parentID {
				t.Fatalf("terminal receipt lost scheduler identity: %+v", n)
			}
			runCmd(m.deliverNotice(n))
			rendered := ansi.Strip(chat.RenderSingleMessage(m.messageRenderParams(), len(m.conv.Messages)-1))
			if !strings.Contains(rendered, "Native children") || strings.Contains(rendered, "fourth hidden detail") {
				t.Fatalf("scheduler notice lost title or leaked intermediate output: %s", rendered)
			}
			if status == "completed" {
				for _, previewLines := range []int{0, 1, 3, 50} {
					params := m.messageRenderParams()
					params.AgentPreviewLines = previewLines
					got := ansi.Strip(chat.RenderSingleMessage(params, len(m.conv.Messages)-1))
					if got != rendered {
						t.Fatalf("scheduler answer depends on child preview setting %d:\n%s\n%s", previewLines, rendered, got)
					}
				}
				for _, marker := range []string{conclusion, "Report detail 0001", "Report detail 3500", "Report detail 7000", reportTail} {
					if !strings.Contains(rendered, marker) {
						t.Fatalf("full scheduler report lost %q", marker)
					}
				}
				if !strings.Contains(rendered, "\n\n  "+conclusion) || strings.Contains(rendered, "Flow result") || strings.Count(rendered, "● Flow") != 1 || strings.Contains(rendered, "More:") || strings.Contains(rendered, "/flow inspect") {
					t.Fatalf("full report should follow the receipt in the native body gutter without a repeated heading: %.1000s", rendered)
				}
				for _, line := range strings.Split(rendered, "\n") {
					if ansi.StringWidth(line) > m.env.Width {
						t.Fatalf("full report line exceeds viewport width: %q", line)
					}
					if strings.Contains(line, conclusion) && !strings.HasPrefix(line, "  ") {
						t.Fatalf("report is missing the native message body gutter: %q", line)
					}
				}
			} else if strings.Contains(rendered, conclusion) {
				t.Fatalf("scheduler start invented a final answer: %s", rendered)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("missing scheduler %s receipt", status)
		}
	}
	deliverScheduler("running")
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
		rows, _ := m.renderTrackerList()
		view := ansi.Strip(rows)
		m.workerMu.Unlock()
		if !strings.Contains(view, "Native children") {
			t.Fatalf("scheduler graph missing from task area: %s", view)
		}
		if strings.Contains(view, "Tasks") || strings.Contains(view, "native-children/") {
			t.Fatalf("managed children duplicated in footer: %s", view)
		}
		params := m.messageRenderParams()
		live := ansi.Strip(chat.RenderActiveBody(params))
		for _, name := range names {
			if !strings.Contains(live, "Agent: "+name) || !strings.Contains(live, "Thinking...") {
				t.Fatalf("native worker %s missing from conversation: %s", name, live)
			}
			found := false
			for _, bg := range m.features.Task.ListRunning() {
				info := bg.GetStatus()
				if info.ParentTaskID != parentID || !strings.Contains(info.Description, name) {
					continue
				}
				if info.ParentSessionID != m.sessionID() {
					t.Fatalf("wrong child session: %+v", info)
				}
				m.workerMu.Lock()
				tracked := false
				for _, item := range m.features.Tracker.List() {
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
			expected := "actual answer"
			if strings.Contains(n.Display, `description="report"`) {
				// Ordinary child notifications retain their existing size guard;
				// only the scheduler's independent final report bypasses it.
				expected = "too large to inline"
			}
			if n.Content != "" || !n.FromAgent || n.FinishedTaskID == "" || n.SessionID != m.sessionID() || !strings.Contains(n.Display, expected) {
				t.Fatalf("not a display-only native answer: %+v", n)
			}
			runCmd(m.deliverNotice(n))
			msg := m.conv.Messages[len(m.conv.Messages)-1]
			if msg.Role != core.ChatNotice || !msg.AgentNotice || !strings.Contains(msg.Content, expected) {
				t.Fatalf("child result not shown as native notice: %+v", msg)
			}
			rendered := ansi.Strip(chat.RenderSingleMessage(m.messageRenderParams(), len(m.conv.Messages)-1))
			if !strings.Contains(rendered, expected) || strings.Contains(rendered, "fourth hidden detail") || !strings.Contains(rendered, "Agent:") {
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
	deliverScheduler("completed")
	record, err := svc.GetRun(parentID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != flow.RunSucceeded || len(record.Nodes) != 4 {
		t.Fatalf("wrong final checkpoint: %+v", record)
	}
	if got := parent.GetStatus().Result; got != reportOutput {
		t.Fatalf("scheduler Result differs from full sink answer: got %d bytes, want %d", len(got), len(reportOutput))
	}
	if got := parent.GetStatus().Output; got != reportOutput[:taskOutputLimit] {
		t.Fatalf("scheduler Output no longer preserves bounded prefix: got %d bytes", len(got))
	}
	if got := record.Nodes["report"].Output; got != reportOutput {
		t.Fatalf("authoritative sink checkpoint lost full report: got %d bytes, want %d", len(got), len(reportOutput))
	}
	if !strings.Contains(record.Output, conclusion) || !strings.Contains(record.Output, "TAIL DISCLAIMER") || record.Output == reportOutput {
		t.Fatalf("inspect lost full status summary and final answer: %s", record.Output)
	}
	for name, checkpoint := range record.Nodes {
		identity := checkpoint.Execution
		bg, ok := m.features.Task.Get(identity.TaskID)
		if !ok || identity.SessionID == "" {
			t.Fatalf("%s has no native execution: %+v", name, checkpoint)
		}
		info := bg.GetStatus()
		if info.ParentTaskID != parentID || info.ID != identity.TaskID || info.AgentSessionID != identity.SessionID {
			t.Fatalf("checkpoint identity diverged: %+v %+v", identity, info)
		}
		child, err := m.features.SessionStore.Read(t.Context(), identity.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		if len(child.Messages()) == 0 {
			t.Fatalf("%s child session has no actual conversation", name)
		}
	}
	snapshot := m.features.Session.Snapshot()
	select {
	case req := <-mainProvider.requests:
		t.Fatalf("managed flow children woke main model: %+v", req)
	default:
	}
	if got := len(snapshot.Messages()); got != 0 {
		t.Fatalf("display-only results added %d model messages", got)
	}
	saved, err := m.features.SessionStore.Read(t.Context(), m.sessionID())
	if err != nil {
		t.Fatal(err)
	}
	restored := m.features.SessionStore.ChatRows(saved)
	if len(restored) != 6 || len(m.conv.Messages) != 6 {
		t.Fatalf("persisted lifecycle annotations=%d, live=%d", len(restored), len(m.conv.Messages))
	}
	for i, row := range restored {
		if row.Role != core.ChatNotice || !row.AgentNotice || row.Content != m.conv.Messages[i].Content {
			t.Fatalf("restored native result differs: %+v", row)
		}
		got := chat.RenderSingleMessage(chat.RenderContext{Messages: restored, Width: 80, AgentPreviewLines: 3}, i)
		want := chat.RenderSingleMessage(m.messageRenderParams(), i)
		if got != want {
			t.Fatalf("restored rendering differs:\n%s\n%s", got, want)
		}
	}
}
