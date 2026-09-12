package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

// stubProvider is a minimal llm.Provider that ends every turn immediately, so a
// Session can start without a real backend. What it owns is reaching the
// endpoint; the model behind it is the SDK's test double.
type stubProvider struct{}

func (stubProvider) Client(string, map[string]string) (*ai.Client, error) {
	return aitest.Always(aitest.Replies(ai.Response{StopReason: ai.StopEndTurn})).Client(), nil
}
func (stubProvider) ListModels(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (stubProvider) Name() string                                        { return "stub" }

// A mid-conversation rebuild reseeds the replacement agent from the outgoing
// one's chain (see ensureAgentSession), so Session must surface that chain with
// message IDs intact — otherwise the rebuilt agent loses the conversation.
func TestSessionMessagesReturnsLiveChainWithIDs(t *testing.T) {
	sess := &Session{}
	seed := []core.Message{
		{ID: "u1", Role: ai.RoleUser, Content: ai.TextContent("survey internal/broker")},
		{ID: "a1", Role: ai.RoleAssistant, Content: ai.TextContent("here is what I found")},
	}
	if err := sess.Start(BuildParams{Provider: stubProvider{}, ModelID: "m"}, seed); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sess.Stop()

	got := sess.Messages()
	if len(got) != 2 || got[0].ID != "u1" || got[1].ID != "a1" {
		t.Fatalf("Messages() = %+v, want the seeded chain with ids preserved", got)
	}
}

// A cold start has no live agent, so Messages() reports nothing and
// ensureAgentSession falls back to the UI conversation for its seed.
func TestSessionMessagesNilWhenInactive(t *testing.T) {
	if got := (&Session{}).Messages(); got != nil {
		t.Fatalf("Messages() on an inactive session = %+v, want nil", got)
	}
}

// controlledAgent is a core.Agent whose Run exits only when the test says so.
// ignoreContext models a runner that outlives its cancellation.
type controlledAgent struct {
	inbox         chan core.Inbound
	outbox        chan core.Event
	started       chan struct{}
	release       chan struct{}
	ignoreContext bool
}

func newControlledAgent(ignoreContext bool) *controlledAgent {
	return &controlledAgent{
		inbox:         make(chan core.Inbound, 8),
		outbox:        make(chan core.Event, 8),
		started:       make(chan struct{}),
		release:       make(chan struct{}),
		ignoreContext: ignoreContext,
	}
}

func (a *controlledAgent) ID() string                                     { return "controlled" }
func (a *controlledAgent) System() core.System                            { return nil }
func (a *controlledAgent) Tools() core.Tools                              { return nil }
func (a *controlledAgent) Inbox() chan<- core.Inbound                     { return a.inbox }
func (a *controlledAgent) Outbox() <-chan core.Event                      { return a.outbox }
func (a *controlledAgent) Messages() []core.Message                       { return nil }
func (a *controlledAgent) SetMessages([]core.Message)                     {}
func (a *controlledAgent) Append(context.Context, core.Message)           {}
func (a *controlledAgent) ThinkAct(context.Context) (*core.Result, error) { return nil, nil }
func (a *controlledAgent) Run(ctx context.Context) error {
	close(a.started)
	if a.ignoreContext {
		<-a.release
	} else {
		select {
		case <-ctx.Done():
		case <-a.release:
		}
	}
	return nil
}
func (a *controlledAgent) InterruptCurrentTurn() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

func sessionWithAgents(agents ...*controlledAgent) *Session {
	next := 0
	return &Session{build: func(BuildParams) (core.Agent, *PermissionGate, error) {
		ag := agents[next]
		next++
		return ag, nil, nil
	}}
}

func TestSessionStopWaitsForExactRunBeforeRestart(t *testing.T) {
	first := newControlledAgent(true)
	second := newControlledAgent(false)
	sess := sessionWithAgents(first, second)
	if err := sess.Start(BuildParams{}, nil); err != nil {
		t.Fatalf("Start first: %v", err)
	}
	<-first.started

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := sess.StopContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StopContext error = %v, want deadline exceeded", err)
	}
	if !sess.Active() {
		t.Fatal("timed-out stop cleared a runner that is still alive")
	}
	if err := sess.Start(BuildParams{}, nil); err == nil {
		t.Fatal("Start succeeded before the previous runner exited")
	}

	close(first.release)
	sess.Stop() // returns once the released runner has retired
	if sess.Active() {
		t.Fatal("session remained active after its runner exited")
	}
	if err := sess.Start(BuildParams{}, nil); err != nil {
		t.Fatalf("Start second after first exited: %v", err)
	}
	<-second.started
	sess.Stop()
}

func TestSessionSendPreservesMessageIdentity(t *testing.T) {
	ag := newControlledAgent(false)
	sess := sessionWithAgents(ag)
	if err := sess.Start(BuildParams{}, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-ag.started

	want := core.Message{ID: "ui-message-1", Role: ai.RoleUser, Content: ai.TextContent("hello")}
	if err := sess.Send(want); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := (<-ag.inbox).Msg
	if got.ID != want.ID || got.Text() != want.Text() {
		t.Fatalf("delivered message = %+v, want ID %q and content %q", got, want.ID, want.Text())
	}
	sess.Stop()
}

func TestSessionUnexpectedExitClearsLifecycle(t *testing.T) {
	ag := newControlledAgent(true)
	sess := sessionWithAgents(ag)
	if err := sess.Start(BuildParams{}, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-ag.started
	close(ag.release)
	sess.Stop()
	if sess.Active() {
		t.Fatal("session remained active after its runner exited")
	}
	if err := sess.Send(core.Message{}); !errors.Is(err, ErrSessionInactive) {
		t.Fatalf("Send after exit error = %v, want ErrSessionInactive", err)
	}
}
