package session

import (
	"context"
	"fmt"
	"slices"

	"github.com/genai-io/sdk-go/pkg/ai"
)

// AcceptInput durably owns exact prepared input before native queue admission.
// Conversation output stays ahead of queued input until MessageAdded consumes it.
func (s *Session) AcceptInput(ctx context.Context, message ai.Message, source *EventRef) error {
	return s.Append(ctx, Event{Source: source, Payload: MessageAppendPayload{Messages: []ai.Message{message}, Accepted: true}})
}

func (p *Projection) consumeInputEvents(messages []ai.Message) ([]Event, error) {
	var events []Event
	for _, message := range messages {
		if seq := p.data.Accepted[message.ID]; seq != 0 {
			if !sameJSON(p.message(message.ID), message) {
				return nil, fmt.Errorf("consumed input differs from accepted message %s", message.ID)
			}
			events = append(events, Event{Source: &EventRef{Seq: seq}, Payload: MessageAppendPayload{Messages: []ai.Message{message}}})
		}
	}
	return events, nil
}

// orderHistoryMessage keeps full history in native consumption order too, while
// preserving accepted-but-unconsumed input at its recoverable tail.
func (p *Projection) orderHistoryMessage(id string, accepted bool) {
	index := p.messageIndex[id]
	if index == len(p.data.Messages)-1 && (accepted || len(p.data.Accepted) == 0) {
		return
	}
	message := p.data.Messages[index]
	p.data.Messages = slices.Delete(p.data.Messages, index, index+1)
	at := len(p.data.Messages)
	if !accepted {
		if pending := slices.IndexFunc(p.data.Messages, func(m ai.Message) bool { return p.data.Accepted[m.ID] != 0 }); pending >= 0 {
			at = pending
		}
	}
	p.data.Messages = slices.Insert(p.data.Messages, at, message)
	for i := min(index, at); i < len(p.data.Messages); i++ {
		p.messageIndex[p.data.Messages[i].ID] = i
	}
}

func (p *Projection) consumedMessages() []ai.Message {
	var messages []ai.Message
	for _, id := range p.data.ActiveIDs {
		if p.data.Accepted[id] == 0 {
			messages = append(messages, p.message(id))
		}
	}
	return messages
}
