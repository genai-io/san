package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/acp"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
)

// Foreground tools must join before prompt exit clears the turn; background
// workers and shells must never use this adapter.
type acpInput struct {
	mu            sync.Mutex
	turn          *acp.Turn
	promptContext context.Context
	cancelTurn    func()
}

func (i *acpInput) set(t *acp.Turn)    { i.mu.Lock(); i.turn = t; i.mu.Unlock() }
func (i *acpInput) current() *acp.Turn { i.mu.Lock(); defer i.mu.Unlock(); return i.turn }

// bindCancellation fences a cancelled foreground interaction before its native
// tool returns, so the actor cannot start another inference before the host's
// event consumer observes the same prompt cancellation.
func (i *acpInput) bindCancellation(ctx context.Context, cancel func()) {
	i.mu.Lock()
	i.promptContext, i.cancelTurn = ctx, cancel
	i.mu.Unlock()
}

func (i *acpInput) cancellationFence() func() {
	i.mu.Lock()
	ctx, cancel := i.promptContext, i.cancelTurn
	i.mu.Unlock()
	return func() {
		if ctx != nil && ctx.Err() != nil && cancel != nil {
			cancel()
		}
	}
}

func acpForm(ctx context.Context, turn *acp.Turn, message string, properties map[string]any, required []string) (map[string]json.RawMessage, bool, error) {
	if turn == nil {
		return nil, false, errors.New("ACP input is unavailable outside the foreground prompt")
	}
	props, err := json.Marshal(properties)
	if err != nil {
		return nil, false, err
	}
	res, err := turn.CreateElicitationContext(ctx, protocol.CreateElicitationRequest{Value: protocol.CreateElicitationRequestFormVariant{
		Mode: "form", Message: message, RequestedSchema: protocol.ElicitationSchema{Type: protocol.Some(protocol.ElicitationSchemaTypeObject), Properties: protocol.Some(json.RawMessage(props)), Required: protocol.Some(required)},
	}})
	if err != nil {
		return nil, false, err
	}
	switch v := res.Value.(type) {
	case protocol.CreateElicitationResponseCancelVariant, protocol.CreateElicitationResponseDeclineVariant:
		return nil, true, nil
	case protocol.CreateElicitationResponseAcceptVariant:
		var content map[string]json.RawMessage
		if !v.Content.Present || v.Content.Null || json.Unmarshal(v.Content.Value, &content) != nil || content == nil {
			return nil, false, errors.New("invalid ACP form content")
		}
		for key := range content {
			if _, ok := properties[key]; !ok {
				return nil, false, errors.New("unknown ACP form field")
			}
		}
		for _, key := range required {
			if _, ok := content[key]; !ok {
				return nil, false, errors.New("missing ACP form field")
			}
		}
		return content, false, nil
	default:
		return nil, false, errors.New("unsupported ACP elicitation action")
	}
}

func (i *acpInput) ask(ctx context.Context, req *tool.QuestionRequest) (*tool.QuestionResponse, error) {
	props := map[string]any{}
	var required []string
	for n, q := range req.Questions {
		if q.MultiSelect {
			return nil, errors.New("ACP native questions require single selections")
		}
		key := fmt.Sprintf("q%d", n)
		choices := make([]map[string]string, 0, len(q.Options)+1)
		for j, option := range q.Options {
			choices = append(choices, map[string]string{"const": fmt.Sprint(j), "title": option.Label})
		}
		choices = append(choices, map[string]string{"const": "other", "title": "Other (enter text below)"})
		props[key] = map[string]any{"type": "string", "title": q.Question, "oneOf": choices}
		props[key+"_text"] = map[string]any{"type": "string", "title": q.Question + " — Other", "description": "Use only with Other. Do not enter passwords or secrets."}
		required = append(required, key)
	}
	fenceCancellation := i.cancellationFence()
	content, cancelled, err := acpForm(ctx, i.current(), "Answer the questions. Do not enter passwords or secrets.", props, required)
	fenceCancellation()
	if err != nil {
		return nil, err
	}
	response := &tool.QuestionResponse{RequestID: req.ID, Cancelled: cancelled, Answers: map[int][]string{}}
	if cancelled {
		return response, nil
	}
	for n, q := range req.Questions {
		key := fmt.Sprintf("q%d", n)
		var selected, text string
		if json.Unmarshal(content[key], &selected) != nil {
			return nil, errors.New("invalid ACP selection")
		}
		if raw, ok := content[key+"_text"]; ok {
			if string(raw) == "null" || json.Unmarshal(raw, &text) != nil {
				return nil, errors.New("invalid ACP free text")
			}
		}
		answer := ""
		if selected == "other" {
			answer = strings.TrimSpace(text)
		} else {
			if text != "" {
				return nil, errors.New("free text requires Other selection")
			}
			for j, option := range q.Options {
				if selected == fmt.Sprint(j) {
					answer = option.Label
					break
				}
			}
		}
		if answer == "" {
			return nil, errors.New("unknown or empty ACP answer")
		}
		response.Answers[n] = []string{answer}
	}
	return response, nil
}

func (i *acpInput) responder(context.Context) tool.BashPromptResponder { return i }
func (i *acpInput) RequestAnswer(ctx context.Context, command, prompt string) (string, bool) {
	fenceCancellation := i.cancellationFence()
	content, cancelled, err := acpForm(ctx, i.current(), "Command: "+command+"\n"+prompt+"\nDo not enter passwords or secrets.", map[string]any{"answer": map[string]any{"type": "string", "title": "Input"}}, []string{"answer"})
	fenceCancellation()
	if err != nil || cancelled {
		return "", false
	}
	var answer string
	if string(content["answer"]) == "null" || json.Unmarshal(content["answer"], &answer) != nil || strings.ContainsAny(answer, "\r\n\x00") {
		return "", false
	}
	return answer, true
}

// ACP's pinned form schema has no password/masked-input format. URL flows
// cannot supply a local child's stdin. Never downgrade secrets to plain forms.
func (*acpInput) RequestSecret(context.Context, string) (string, bool) { return "", false }
