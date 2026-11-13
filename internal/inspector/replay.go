package inspector

import (
	"context"
	"fmt"

	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/sdk-go/pkg/ai"
)

type nativeState struct {
	Seq      uint64                       `json:"seq"`
	Event    *session.Event               `json:"event,omitempty"`
	Identity session.SessionCreatePayload `json:"identity"`
	Context  *ai.Request                  `json:"context"`
	Request  *ai.Request                  `json:"request,omitempty"`
	History  []ai.Message                 `json:"history"`
}

func (s *Server) readState(ctx context.Context, id string, seq uint64) (nativeState, error) {
	p, err := s.store.Read(ctx, id)
	if err != nil {
		return nativeState{}, err
	}
	events := p.Events()
	st := nativeState{Seq: seq, Identity: p.Identity(), History: p.Messages()}
	if seq != 0 {
		if seq > uint64(len(events)) {
			return nativeState{}, fmt.Errorf("event sequence out of range")
		}
		event := events[seq-1]
		st.Event = &event
	}
	st.Context, err = s.store.ContextRequest(ctx, id, seq)
	if err != nil {
		return nativeState{}, err
	}
	if st.Event != nil && st.Event.Type == session.EventInferenceStart {
		st.Request, err = s.store.InferenceRequest(ctx, id, seq)
	}
	return st, err
}
