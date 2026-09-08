package agent

import (
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"

	"context"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
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
