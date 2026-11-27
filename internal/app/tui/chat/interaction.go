package chat

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/ask"
	"github.com/genai-io/san/internal/runtime"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
)

// UserPromptRequest keeps the TUI transport's existing message vocabulary while
// the request contract and lifecycle belong to interaction.
type UserPromptRequest = ask.Request

type UserPromptRequestMsg struct{ Request *UserPromptRequest }

var userPromptSequence atomic.Uint64
var ErrInteractionUnavailable = errors.New("session UI is closed or unavailable")

func (h *AgentToUI) interaction(ctx context.Context) (*UserPromptRequest, func(), error) {
	if h == nil || h.ctx.Err() != nil {
		return nil, nil, ErrInteractionUnavailable
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(h.ctx, cancel)
	return &UserPromptRequest{ID: userPromptSequence.Add(1), Context: ctx, Owner: h}, func() { stop(); cancel() }, nil
}

func (h *AgentToUI) userPromptError(ctx context.Context) error {
	if err := h.ctx.Err(); err != nil {
		return err
	}
	return ctx.Err()
}

func (h *AgentToUI) sendUserPrompt(req *UserPromptRequest) error {
	if err := h.userPromptError(req.Context); err != nil {
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
	resolved := h.ResolveApproval(ctx, source, view, input)
	return resolved.Decision == "allow", resolved.Reason
}

// ResolveApproval preserves the native verdict and the question actually shown.
func (h *AgentToUI) ResolveApproval(ctx context.Context, source tool.AgentInteractionSource, view *perm.PermissionRequest, input map[string]any) perm.Resolution {
	cancelled := func(err error, asked *perm.Asked) perm.Resolution {
		return perm.Resolution{Decision: "cancelled", By: "user", Reason: err.Error(), Asked: asked}
	}
	req, cancel, err := h.interaction(ctx)
	if err != nil {
		return cancelled(err, nil)
	}
	defer cancel()
	req.Source = source
	req.PermissionView = view
	req.Permission = &runtime.PermGateRequest{
		Context: req.Context, RequestID: view.ID, ToolCallID: source.ToolCallID,
		ToolName: view.ToolName, Description: view.Description, Input: input,
		Response: make(chan runtime.PermGateResponse, 1),
	}
	if err := h.sendUserPrompt(req); err != nil {
		return cancelled(err, req.Permission.AskedSnapshot())
	}
	select {
	case response := <-req.Permission.Response:
		asked := response.Asked
		if asked == nil {
			asked = req.Permission.AskedSnapshot()
		}
		if err := h.userPromptError(req.Context); err != nil {
			return cancelled(err, asked)
		}
		decision := "deny"
		if response.Cancelled {
			decision = "cancelled"
		} else if response.Allow {
			decision = "allow"
		}
		return perm.Resolution{Decision: decision, By: response.By, Scope: response.Scope, Reason: response.Reason, Asked: asked}
	case <-req.Context.Done():
		return cancelled(req.Context.Err(), req.Permission.AskedSnapshot())
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
	if err := h.sendUserPrompt(req); err != nil {
		return nil, err
	}
	select {
	case response := <-req.Question.Reply:
		if err := h.userPromptError(req.Context); err != nil {
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
	if err := h.sendUserPrompt(req); err != nil {
		return "", false, err
	}
	select {
	case response := <-req.Secret.Reply:
		if err := h.userPromptError(req.Context); err != nil {
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
			return UserPromptRequestMsg{Request: req}
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
