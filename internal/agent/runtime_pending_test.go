package agent

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"github.com/genai-io/san/internal/core"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type queuedFailureProvider struct {
	stubProvider
	entered, release chan struct{}
}

func (p *queuedFailureProvider) Client(string, map[string]string) (*ai.Client, error) {
	return aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		close(p.entered)
		select {
		case <-p.release:
		case <-ctx.Done():
		}
		return aitest.Fails(&ai.Error{Kind: ai.KindAuth, Message: "test provider failure"})(ctx, req)
	}).Client(), nil
}
func TestAcceptedInputSurvivesFailedRun(t *testing.T) {
	p := &queuedFailureProvider{entered: make(chan struct{}), release: make(chan struct{})}
	rt := &Runtime{}
	stopped := make(chan struct{}, 1)
	var recorded []core.Message
	var lateErr error
	if err := rt.Start(BuildParams{Provider: p, ModelID: "m", OnEvent: func(ev core.Event) {
		if e, ok := ev.(sdkagent.MessageAdded); ok {
			recorded = append(recorded, e.Message)
		}
		if _, ok := ev.(core.AgentStopped); ok {
			lateErr = rt.Send(core.UserMessage("too late", nil))
			stopped <- struct{}{}
		}
	}}, nil); err != nil {
		t.Fatal(err)
	}
	defer rt.Stop()
	if err := rt.Send(core.Message{ID: "first", Role: ai.RoleUser, Content: ai.TextContent("first prompt")}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.entered:
	case <-time.After(time.Second):
		t.Fatal("inference never started")
	}
	if err := rt.Send(core.Message{ID: "second", Role: ai.RoleUser, Content: ai.TextContent("accepted while inference runs")}); err != nil {
		t.Fatal(err)
	}
	close(p.release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("run did not stop")
	}
	rt.Stop()
	if !errors.Is(lateErr, ErrRuntimeStopped) {
		t.Fatalf("stopped mailbox accepted more input: %v", lateErr)
	}
	for _, messages := range [][]core.Message{rt.Messages(), recorded} {
		if len(messages) != 2 || messages[0].ID != "first" || messages[1].ID != "second" {
			t.Fatalf("accepted input must survive exactly once in history and recording: %+v", messages)
		}
	}
}

func TestAcceptedInputSurvivesStopBeforeFirstTurn(t *testing.T) {
	ag := newControlledAgent(false)
	rt := sessionWithAgents(ag)
	var recorded []core.Message
	if err := rt.Start(BuildParams{OnEvent: func(ev core.Event) {
		if e, ok := ev.(sdkagent.MessageAdded); ok {
			recorded = append(recorded, e.Message)
		}
	}}, nil); err != nil {
		t.Fatal(err)
	}
	defer rt.Stop()
	if err := rt.Send(core.UserMessage("not yet read by the agent", nil)); err != nil {
		t.Fatal(err)
	}
	rt.Stop()
	messages := rt.Messages()
	if len(messages) != 1 || messages[0].ID == "" || len(recorded) != 1 || messages[0].ID != recorded[0].ID {
		t.Fatalf("input accepted before shutdown was not recorded: history=%+v, events=%+v", messages, recorded)
	}
}
