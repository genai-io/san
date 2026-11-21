// Side requests: one-off model calls answered from the conversation as it
// stands. A side request reuses the main agent's system prompt, tools and chain,
// so the provider's prompt cache serves the prefix; neither the prompt nor the
// reply enters the agent's chain, so the main agent never sees them. /btw,
// Suggest and Recap are side requests.
package app

import (
	"context"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/sidecard"
)

// sideRequest couples a captured Side Card with the UI's result owner.
type sideRequest struct {
	label    string
	snapshot sidecard.Snapshot
	chainLen int
	owner    *session.Session
}

func (m *model) newSideRequest(label string, advertiseTools bool, prompt string, extra ...string) sideRequest {
	client, chain := m.buildLLMClient(), m.services.Agent.Messages()
	var system string
	var schemas []core.ToolSchema
	if sys, tools := m.services.Agent.System(), m.services.Agent.Tools(); sys != nil && tools != nil {
		system = sys.Prompt()
		if advertiseTools {
			schemas = tools.Schemas()
		}
	} else {
		params := m.promptParams()
		system = params.System().Prompt()
		if advertiseTools {
			schemas = params.ToolSchemas()
		}
	}
	owner := m.services.Session
	snapshot := sidecard.NewSnapshot(sidecard.SnapshotOptions{
		Client: client, System: system, Tools: schemas, Messages: chain, Owner: owner, Purpose: label,
	}, prompt, extra...)
	return sideRequest{label: label, snapshot: snapshot, chainLen: len(chain), owner: owner}
}

func (r sideRequest) complete(ctx context.Context) (llm.CompletionResponse, error) {
	resp, err := r.snapshot.Complete(ctx)
	if err == nil {
		log.Logger().Debug("side request answered", zap.String("label", r.label), zap.Int("input", resp.Usage.Input), zap.Int("cache_read", resp.Usage.CacheRead), zap.Int("cache_write", resp.Usage.CacheWrite), zap.Int("output", resp.Usage.Output))
	}
	return resp, err
}

// closeSide resolves discarded attempts against their original owner, even after a switch.
func closeSide(c *session.Completion) {
	if err := c.Close(); err != nil {
		log.Logger().Error("close side recording", zap.Error(err))
	}
}

func finishSide(c *session.Completion, text string) bool {
	var events []session.Event
	if text != "" {
		events = append(events, session.Event{Payload: session.AnnotationAddPayload{Text: text}})
	}
	if err := c.Finish(context.Background(), events...); err != nil {
		log.Logger().Error("finish side recording", zap.Error(err))
		return false
	}
	return true
}
