package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/app/input"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/mcp"
	"github.com/genai-io/san/internal/skill"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"

	"github.com/genai-io/san/internal/flow"
	"github.com/genai-io/san/internal/session"
)

func flowMainTestSession(t *testing.T) *session.Session {
	t.Helper()
	m, _ := resumedModel(t, nil)
	return m.services.Session
}

func TestFlowMainLeaseExclusiveAndGenerationFenced(t *testing.T) {
	b := newFlowMainBridge()
	owner := flowMainTestSession(t)
	e := b.bind(owner)
	first, err := e.Open(context.Background(), flow.MainRunInfo{SessionID: e.owner.ID()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Open(context.Background(), flow.MainRunInfo{SessionID: e.owner.ID()}); err == nil {
		t.Fatal("parallel main lease admitted")
	}
	first.Close(nil)
	(&model{flowMain: b, services: services{Session: owner}}).onFlowMain(<-b.events)
	second, err := e.Open(context.Background(), flow.MainRunInfo{SessionID: e.owner.ID()})
	if err != nil {
		t.Fatal(err)
	}
	b.bind(flowMainTestSession(t))
	select {
	case <-second.(*flowMainLease).done:
	default:
		t.Fatal("session replacement did not retire lease")
	}
	if _, err = e.Open(context.Background(), flow.MainRunInfo{SessionID: e.owner.ID()}); err == nil {
		t.Fatal("stale executor admitted")
	}
	// Rebinding even the same session ID via a different opened handle is fenced.
	b.bind(owner)
	if _, err = e.Open(context.Background(), flow.MainRunInfo{SessionID: e.owner.ID()}); err == nil {
		t.Fatal("old generation admitted")
	}
}

func TestFlowMainOpenNonblockingConcurrent(t *testing.T) {
	b := newFlowMainBridge()
	e := b.bind(flowMainTestSession(t))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var leases []flow.MainRunExecutor
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := e.Open(context.Background(), flow.MainRunInfo{SessionID: e.owner.ID()})
			if err == nil {
				mu.Lock()
				leases = append(leases, l)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(leases) != 1 {
		t.Fatalf("admitted %d leases", len(leases))
	}
	leases[0].Close(nil)
}

func TestFlowMainReviewQueuedRequirementsOverrideAcceptance(t *testing.T) {
	m, _ := compactQueueModel(t)
	m.flowMain = newFlowMainBridge()
	e := m.flowMain.bind(m.services.Session)
	l, err := e.Open(context.Background(), flow.MainRunInfo{SessionID: e.owner.ID()})
	if err != nil {
		t.Fatal(err)
	}
	m.userInput.Queue.Enqueue("new requirement", nil)
	reply := make(chan flowMainReply, 1)
	m.onFlowMain(flowMainMsg{lease: l.(*flowMainLease), ctx: context.Background(), op: flowMainReview, accepted: true, reply: reply})
	if r := <-reply; !r.revise || r.err != nil {
		t.Fatalf("review = %+v", r)
	}
	if !m.flowMainCapturing() {
		t.Fatal("PASS sealed despite new requirements")
	}
	if _, released := m.releaseQueuedMessage(); released {
		t.Fatal("review released input")
	}
	m.userInput.Queue.Dequeue()
	m.onFlowMain(flowMainMsg{lease: l.(*flowMainLease), ctx: context.Background(), op: flowMainReview, accepted: true, reply: reply})
	if r := <-reply; r.revise || r.err != nil {
		t.Fatalf("review = %+v", r)
	}
	if m.flowMainCapturing() {
		t.Fatal("acceptance did not seal input capture")
	}
	if _, err := e.Open(context.Background(), flow.MainRunInfo{SessionID: e.owner.ID()}); err == nil {
		t.Fatal("sealed Flow lost main lease exclusivity")
	}
	select {
	case <-l.(*flowMainLease).done:
		t.Fatal("acceptance retired later main final sink")
	default:
	}
	l.Close(nil)
	if !m.flowMainCapturing() {
		t.Fatal("close released accepted Flow before UI consumed its outcome")
	}
	m.onFlowMain(<-m.flowMain.events)
	m.flowMain.bind(nil)
}

func TestFlowMainNativeRuntimeResultAndIdentity(t *testing.T) { testFlowMainNativeRuntime(t, true) }
func TestFlowMainFreshRuntimeResultAndIdentity(t *testing.T)  { testFlowMainNativeRuntime(t, false) }
func testFlowMainNativeRuntime(t *testing.T, warm bool) {
	m, _ := resumedModel(t, []core.Message{core.UserMessage("existing main history", nil)})
	m.userInput = input.New(m.env.CWD, 80, nil, input.SelectorDeps{Setting: m.services.Setting})
	m.flowMain = newFlowMainBridge()
	opened := m.services.Session
	m.agentSession = opened
	scripted := aitest.Always(aitest.Says("native writer result"))
	p := &finalizationProvider{client: scripted.Client()}
	m.env.LLMProvider = p
	m.services.Skill = skill.NewRegistry()
	m.services.Tool = tool.NewRegistry()
	m.services.MCP = mcp.NewManagerForTest(nil)
	if warm {
		if err := m.services.Agent.Start(agent.BuildParams{Provider: p, ModelID: "m", Recording: opened, PermissionRules: (agent.PermissionPolicy{}).Decide}, m.services.Agent.Messages()); err != nil {
			t.Fatal(err)
		}
		m.agentEvolveCaps = m.selfLearnCapabilities()
		m.agentDisabledToolsSignature = m.disabledToolsSignature()
		m.agentAutoMemory = m.autoMemory()
	} else if m.services.Agent.Active() {
		t.Fatal("fresh fixture already has active runtime")
	}
	e := m.flowMain.bind(opened)
	l, err := e.Open(t.Context(), flow.MainRunInfo{SessionID: e.owner.ID()})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close(nil)
	m.deliverNotice(mainNotice{SessionID: opened.ID(), Destination: opened, Display: "review background notice", Content: "ordinary background machine input"})
	if m.pendingMessages.Len() != 0 || len(l.(*flowMainLease).deferred) != 1 || scripted.Calls() != 0 {
		t.Fatal("review notice entered native pending input or inference")
	}
	reviewReply := make(chan flowMainReply, 1)
	m.onFlowMain(flowMainMsg{lease: l.(*flowMainLease), ctx: t.Context(), op: flowMainReview, accepted: false, reply: reviewReply})
	if review := <-reviewReply; review.err != nil || review.revise {
		t.Fatal("ordinary notice counted as user revision")
	}
	var execution flow.Execution
	m.userInput.Queue.Enqueue("review amendment must reach writer", nil)
	reply := make(chan flowMainReply, 1)
	call := flow.Call{Node: &flow.Node{}, Prompt: "write in the main session", OnExecution: func(e flow.Execution) {
		if scripted.Calls() != 0 {
			t.Error("inference started before execution checkpoint")
		}
		execution = e
	}}
	go func() { output, err := l.RunNode(t.Context(), call); reply <- flowMainReply{output: output, err: err} }()
	select {
	case request := <-m.flowMain.events:
		m.onFlowMain(request)
	case <-time.After(3 * time.Second):
		t.Fatal("main request not registered")
	}
	if execution.SessionID != opened.ID() || execution.MessageID == "" || execution.TaskID != "" {
		t.Fatalf("untruthful execution: %+v", execution)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-m.AgentOutbox():
			conv.Update(m, &m.conv, conv.AgentOutboxMsg{Outbox: m.AgentOutbox(), Event: ev})
		case r := <-reply:
			if r.err != nil || r.output != "native writer result" {
				t.Fatalf("result = %+v", r)
			}
			if m.services.Session != opened {
				t.Fatal("writer changed session")
			}
			foundHistory, foundAmendment := false, false
			for _, msg := range m.services.Agent.Messages() {
				foundHistory = foundHistory || strings.Contains(msg.Text(), "existing main history")
				foundAmendment = foundAmendment || strings.Contains(msg.Text(), "review amendment must reach writer")
			}
			if !foundHistory || !foundAmendment {
				t.Fatal("writer did not retain history and queued amendment")
			}
			if m.flowMainRequest != nil {
				t.Fatal("completed writer still owns turn")
			}
			if m.userInput.Queue.Len() != 0 {
				t.Fatal("unexpected synthetic queue input")
			}
			if strings.Contains(l.Instructions(), "ordinary background") {
				t.Fatal("ordinary background notice polluted critic requirements")
			}
			if strings.Count(l.Instructions(), "review amendment must reach writer") != 1 {
				t.Fatalf("amendment recorded more than once: %q", l.Instructions())
			}
			count := 0
			for _, msg := range m.services.Agent.Messages() {
				if msg.ID == execution.MessageID {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("native writer input duplicated %d times", count)
			}
			m.userInput.Queue.Enqueue("followup after final main result", nil)
			l.Close(nil)
			if !m.flowMainBlocksQueue() {
				t.Fatal("close outcome released queue before UI consumption")
			}
			select {
			case closed := <-m.flowMain.events:
				m.onFlowMain(closed)
			case <-deadline:
				t.Fatal("no close outcome")
			}
			if m.flowMainLease() != nil || m.userInput.Queue.Len() != 0 {
				t.Fatal("successful final sink did not release followup through native queue")
			}
			if len(l.(*flowMainLease).deferred) != 0 || m.pendingMessages.Len() != 0 {
				t.Fatal("successful close did not release ordinary notice")
			}
			shown := 0
			for _, row := range m.conv.Messages {
				if strings.Contains(row.Content, "review background notice") {
					shown++
				}
			}
			if shown != 1 {
				t.Fatalf("background notice displayed %d times", shown)
			}
			return
		case <-deadline:
			t.Fatal("native writer did not finalize")
		}
	}
}

func TestFlowMainBoundCloseHoldsRequirements(t *testing.T) {
	m, _ := compactQueueModel(t)
	m.flowMain = newFlowMainBridge()
	e := m.flowMain.bind(m.services.Session)
	l, err := e.Open(t.Context(), flow.MainRunInfo{SessionID: e.owner.ID()})
	if err != nil {
		t.Fatal(err)
	}
	m.userInput.Queue.Enqueue("unresolved requirement at budget limit", nil)
	m.deliverNotice(mainNotice{SessionID: m.services.Session.ID(), Destination: m.services.Session, Display: "held background notice", Content: "held ordinary machine input"})
	if m.pendingMessages.Len() != 0 || len(l.(*flowMainLease).deferred) != 1 {
		t.Fatal("review notice was not deferred")
	}
	l.Close(context.DeadlineExceeded)
	if m.flowMainLease() == nil || !m.flowMainBlocksQueue() {
		t.Fatal("close prematurely removed admission fence")
	}
	m.onFlowMain(<-m.flowMain.events)
	if m.flowMainLease() != nil || !m.pendingMessages.Held() || m.userInput.Queue.Len() != 1 {
		t.Fatal("bound failure lost or resumed pending requirements")
	}
	if m.pendingMessages.Len() != 1 || len(l.(*flowMainLease).deferred) != 0 {
		t.Fatal("failed close did not preserve ordinary notice held for explicit continuation")
	}
}

func TestFlowMainEditingWaitsAndForeignInfoRejected(t *testing.T) {
	m, _ := compactQueueModel(t)
	m.flowMain = newFlowMainBridge()
	e := m.flowMain.bind(m.services.Session)
	if _, err := e.Open(t.Context(), flow.MainRunInfo{SessionID: "foreign"}); err == nil {
		t.Fatal("foreign session info accepted")
	}
	l, err := e.Open(t.Context(), flow.MainRunInfo{SessionID: e.owner.ID()})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close(nil)
	m.userInput.Queue.Enqueue("editable amendment", nil)
	m.userInput.Queue.SelectIdx = 0
	call := flow.Call{Prompt: "writer"}
	reply := make(chan flowMainReply, 1)
	m.onFlowMain(flowMainMsg{lease: l.(*flowMainLease), ctx: t.Context(), call: &call, reply: reply})
	if m.flowMainRequest == nil || m.flowMainRequest.messageID != "" || m.pendingMessages.Held() {
		t.Fatal("editing failed instead of waiting")
	}
	select {
	case <-reply:
		t.Fatal("writer replied before edit finished")
	default:
	}
	m.userInput.Queue.SelectIdx = -1
	m.startFlowMainNode()
	if m.flowMainRequest == nil || m.flowMainRequest.messageID == "" {
		t.Fatal("writer did not start after editing")
	}
}

func TestFlowMainBusyWaitsWithoutReleasingWriterDirections(t *testing.T) {
	m, _ := compactQueueModel(t)
	m.flowMain = newFlowMainBridge()
	e := m.flowMain.bind(m.services.Session)
	l, err := e.Open(t.Context(), flow.MainRunInfo{SessionID: e.owner.ID()})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close(nil)
	m.conv.Stream.Active = true
	m.userInput.Queue.Enqueue("for writer, not old turn", nil)
	ran := false
	call := flow.Call{Prompt: "writer", OnExecution: func(flow.Execution) { ran = true }}
	reply := make(chan flowMainReply, 1)
	m.onFlowMain(flowMainMsg{lease: l.(*flowMainLease), ctx: t.Context(), call: &call, reply: reply})
	if ran || m.flowMainRequest == nil {
		t.Fatal("busy main was injected instead of waiting")
	}
	m.OnStepEnd()
	if m.userInput.Queue.Len() != 1 {
		t.Fatal("old turn consumed writer directions")
	}
	m.onFlowMain(flowMainMsg{lease: l.(*flowMainLease), op: flowMainCancel, reply: reply})
	if r := <-reply; r.err == nil {
		t.Fatal("cancelled queued writer succeeded")
	}
	if !m.conv.Stream.Active {
		t.Fatal("queued Flow cancellation stopped unrelated old main turn")
	}
	if m.userInput.Queue.Len() != 1 || !m.pendingMessages.Held() {
		t.Fatal("cancelled pending writer lost queue hold")
	}
}

func TestFlowMainBusyStartsAtNativeTurnBoundary(t *testing.T) {
	m, _ := compactQueueModel(t)
	m.flowMain = newFlowMainBridge()
	lease, err := m.flowMain.bind(m.services.Session).Open(t.Context(), flow.MainRunInfo{SessionID: m.sessionID()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.retireFlowMain() })
	m.conv.Stream.Active = true
	m.userInput.Queue.Enqueue("for the next writer only", nil)
	started := false
	call := flow.Call{Prompt: "write", OnExecution: func(flow.Execution) { started = true }}
	m.onFlowMain(flowMainMsg{lease: lease.(*flowMainLease), ctx: t.Context(), call: &call, reply: make(chan flowMainReply, 1)})
	if started {
		t.Fatal("writer interrupted the old main turn")
	}
	m.Update(conv.AgentOutboxMsg{Outbox: m.AgentOutbox(), Event: core.TurnEnded{Result: core.Result{StopReason: core.StopEndTurn}}})
	if !started || m.flowMainRequest == nil || m.flowMainRequest.messageID == "" {
		t.Fatal("normal native turn boundary did not start the waiting writer")
	}
}

func TestFlowMainStaleCancelDoesNotReplyTwice(t *testing.T) {
	m, _ := compactQueueModel(t)
	m.flowMain = newFlowMainBridge()
	lease, err := m.flowMain.bind(m.services.Session).Open(t.Context(), flow.MainRunInfo{SessionID: m.sessionID()})
	if err != nil {
		t.Fatal(err)
	}
	reply := make(chan flowMainReply, 1)
	reply <- flowMainReply{output: "already finished"}
	m.retireFlowMain()
	m.onFlowMain(flowMainMsg{op: flowMainCancel, lease: lease.(*flowMainLease), reply: reply})
	if r := <-reply; r.output != "already finished" {
		t.Fatal("stale cancellation replaced a completed reply")
	}
}

func TestFlowMainCheckpointCancellationRetainsQueue(t *testing.T) {
	m, provider := compactQueueModel(t)
	m.flowMain = newFlowMainBridge()
	e := m.flowMain.bind(m.services.Session)
	lease, err := e.Open(t.Context(), flow.MainRunInfo{SessionID: e.owner.ID()})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close(nil)
	m.userInput.Queue.Enqueue("must not vanish on failed checkpoint", nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	call := flow.Call{Prompt: "writer", OnExecution: func(flow.Execution) { cancel() }}
	reply := make(chan flowMainReply, 1)
	m.onFlowMain(flowMainMsg{lease: lease.(*flowMainLease), ctx: ctx, call: &call, reply: reply})
	r := <-reply
	if r.err == nil {
		t.Fatal("cancelled checkpoint reported success")
	}
	if m.userInput.Queue.Len() != 1 || !m.pendingMessages.Held() {
		t.Fatal("failed checkpoint lost or released input")
	}
	select {
	case <-provider.requests:
		t.Fatal("writer ran before checkpoint committed")
	default:
	}
}

func TestFlowMainCancelledRequestNeverReachesUI(t *testing.T) {
	b := newFlowMainBridge()
	e := b.bind(flowMainTestSession(t))
	l, err := e.Open(context.Background(), flow.MainRunInfo{SessionID: e.owner.ID()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = l.RunNode(ctx, flow.Call{Prompt: "not admitted"})
	if err == nil {
		t.Fatal("cancelled request succeeded")
	}
	l.Close(nil)
}
