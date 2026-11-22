package session

import (
	"context"
	"fmt"

	"github.com/genai-io/sdk-go/pkg/ai"
)

// TurnPrefix captures the final inference of a completed main turn without
// reconstructing its admitted request.
type TurnPrefix struct {
	sessionID string
	seq       uint64
	response  []ai.Message
}

// LastCompletedTurn checks only the newest main inference. A newer inference
// on another route, or one that did not finish with EndTurn, rules out reuse.
func (p *Projection) LastCompletedTurn(provider, model string) (TurnPrefix, bool) {
	for i := len(p.data.Events) - 1; i >= 0; i-- {
		e := p.data.Events[i]
		start, ok := e.Payload.(*InferenceStartPayload)
		if !ok || start.Purpose != PurposeTurn {
			continue
		}
		if start.Request.Provider == nil || *start.Request.Provider != provider ||
			start.Request.Model == nil || *start.Request.Model != model || p.data.UnfinishedInferences[e.Seq] {
			break
		}
		var response []ai.Message
		for _, result := range p.data.Events[i+1:] {
			if result.Source == nil || result.Source.Session != "" || result.Source.Seq != e.Seq {
				continue
			}
			switch v := result.Payload.(type) {
			case *MessageAppendPayload:
				for _, message := range v.Messages {
					if message.Role == ai.RoleAssistant {
						response = append(response, message)
					}
				}
			case *InferenceFinishPayload:
				if v.Outcome == OutcomeSuccess && v.StopReason == string(ai.StopEndTurn) {
					return TurnPrefix{sessionID: p.SessionID(), seq: e.Seq, response: response}, true
				}
				return TurnPrefix{}, false
			}
		}
		break
	}
	return TurnPrefix{}, false
}

// Request reconstructs the admitted request and appends the captured native
// response. Each call returns an independent request without changing history.
func (t TurnPrefix) Request(ctx context.Context, s *Session) (*ai.Request, error) {
	if t.seq == 0 || s == nil || s.ID() != t.sessionID {
		return nil, fmt.Errorf("turn prefix does not belong to this session")
	}
	r, err := s.InferenceRequest(ctx, t.seq)
	if err != nil {
		return nil, err
	}
	if len(t.response) > 0 {
		r.Messages = append(r.Messages, eventCopy(t.response)...)
	}
	return r, nil
}
