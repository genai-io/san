// Conversation-based Asides reuse the main prefix for caching without adding
// their prompts or replies to the main conversation.
package app

import (
	"context"

	"go.uber.org/zap"

	"github.com/genai-io/san/internal/aside"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/log"
	"github.com/genai-io/san/internal/session"
)

// aasideRequest couples a captured Aside with the UI's result owner.
type asideRequest struct {
	label    string
	snapshot aside.Snapshot
	chainLen int
	owner    *session.Session
}

func (m *model) newAsideRequest(label string, advertiseTools bool, prompt string, extra ...string) asideRequest {
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
	snapshot := aside.NewSnapshot(aside.SnapshotOptions{
		Client: client, System: system, Tools: schemas, Messages: chain, Owner: owner, Purpose: label,
	}, prompt, extra...)
	return asideRequest{label: label, snapshot: snapshot, chainLen: len(chain), owner: owner}
}

func (r asideRequest) complete(ctx context.Context) (llm.CompletionResponse, error) {
	resp, err := r.snapshot.Complete(ctx)
	if err == nil {
		log.Logger().Debug("Aside answered", zap.String("label", r.label), zap.Int("input", resp.Usage.Input), zap.Int("cache_read", resp.Usage.CacheRead), zap.Int("cache_write", resp.Usage.CacheWrite), zap.Int("output", resp.Usage.Output))
	}
	return resp, err
}

// closeAside resolves discarded attempts against their original owner, even after a switch.
func closeAside(c *session.Completion) {
	if err := c.Close(); err != nil {
		log.Logger().Error("close Aside recording", zap.Error(err))
	}
}

func finishAside(c *session.Completion, text string) bool {
	var events []session.Event
	if text != "" {
		events = append(events, session.Event{Payload: session.AnnotationAddPayload{Text: text}})
	}
	if err := c.Finish(context.Background(), events...); err != nil {
		log.Logger().Error("finish Aside recording", zap.Error(err))
		return false
	}
	return true
}
