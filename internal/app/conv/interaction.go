package conv

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
)

// InteractionRequest owns one reply endpoint. Only the UI model queues and
// displays it; cancellation and transport closure remain visible after polling.
type InteractionRequest struct {
	ID             uint64
	Context        context.Context
	Owner          *AgentToUI
	Source         tool.AgentInteractionSource
	Permission     *agent.PermGateRequest
	PermissionView *perm.PermissionRequest
	Question       *QuestionRequestMsg
	Secret         *SecretPromptRequestMsg
}

type InteractionRequestMsg struct{ Request *InteractionRequest }

var interactionSequence atomic.Uint64
var ErrInteractionUnavailable = errors.New("session UI is closed or unavailable")

func (h *AgentToUI) interaction(ctx context.Context) (*InteractionRequest, func(), error) {
	if h == nil || h.ctx.Err() != nil {
		return nil, nil, ErrInteractionUnavailable
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(h.ctx, cancel)
	return &InteractionRequest{ID: interactionSequence.Add(1), Context: ctx, Owner: h}, func() { stop(); cancel() }, nil
}

func (h *AgentToUI) interactionError(ctx context.Context) error {
	if err := h.ctx.Err(); err != nil {
		return err
	}
	return ctx.Err()
}

func (h *AgentToUI) sendInteraction(req *InteractionRequest) error {
	if err := h.interactionError(req.Context); err != nil {
		return err
	}
	select {
	case h.requests <- req:
		return nil
	case <-req.Context.Done():
		return req.Context.Err()
	}
}

// Approve forwards only an already evaluated worker Prompt, never a Reject.
func (h *AgentToUI) Approve(ctx context.Context, source tool.AgentInteractionSource, view *perm.PermissionRequest, input map[string]any) (bool, string) {
	req, cancel, err := h.interaction(ctx)
	if err != nil {
		return false, err.Error()
	}
	defer cancel()
	req.Source = source
	req.PermissionView = view
	req.Permission = &agent.PermGateRequest{
		Context: req.Context, RequestID: view.ID, ToolCallID: source.ToolCallID,
		ToolName: view.ToolName, Description: view.Description, Input: input,
		Response: make(chan agent.PermGateResponse, 1),
	}
	if err := h.sendInteraction(req); err != nil {
		return false, err.Error()
	}
	select {
	case response := <-req.Permission.Response:
		if err := h.interactionError(req.Context); err != nil {
			return false, err.Error()
		}
		return response.Allow, response.Reason
	case <-req.Context.Done():
		return false, req.Context.Err().Error()
	}
}

func (h *AgentToUI) Ask(ctx context.Context, source tool.AgentInteractionSource, question *tool.QuestionRequest) (*tool.QuestionResponse, error) {
	req, cancel, err := h.interaction(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	// Each displayed question has its own identity, even if a caller reuses its payload.
	copy := *question
	req.Source = source
	req.Question = &QuestionRequestMsg{Request: &copy, Reply: make(chan *tool.QuestionResponse, 1)}
	if err := h.sendInteraction(req); err != nil {
		return nil, err
	}
	select {
	case response := <-req.Question.Reply:
		if err := h.interactionError(req.Context); err != nil {
			return nil, err
		}
		if response == nil {
			return nil, errors.New("question prompt closed without a response")
		}
		return response, nil
	case <-req.Context.Done():
		return nil, req.Context.Err()
	}
}

func (h *AgentToUI) RequestSecret(ctx context.Context, prompt string) (string, bool, error) {
	req, cancel, err := h.interaction(ctx)
	if err != nil {
		return "", false, err
	}
	defer cancel()
	req.Secret = &SecretPromptRequestMsg{Prompt: prompt, Reply: make(chan SecretPromptResponse, 1)}
	if err := h.sendInteraction(req); err != nil {
		return "", false, err
	}
	select {
	case response := <-req.Secret.Reply:
		if err := h.interactionError(req.Context); err != nil {
			return "", false, err
		}
		return response.Value, !response.Cancelled, nil
	case <-req.Context.Done():
		return "", false, req.Context.Err()
	}
}

// Check keeps progress independent of whichever interaction is awaiting a reply.
func (h *AgentToUI) Check() tea.Cmd {
	if h == nil || h.ctx.Err() != nil {
		return nil
	}
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg {
		select {
		case req := <-h.requests:
			return InteractionRequestMsg{Request: req}
		default:
		}
		if batch := h.takeActivity(); len(batch) > 0 {
			return batch
		}
		return AgentToUITickMsg{}
	})
}

// Closed reports the session lifetime directly, without waiting for child
// contexts to observe the asynchronous cancellation callback.
func (h *AgentToUI) Closed() bool { return h == nil || h.ctx.Err() != nil }

// Close is logical: producers never race a channel close and every waiter exits.
func (h *AgentToUI) Close() {
	if h != nil {
		h.cancel()
	}
}
