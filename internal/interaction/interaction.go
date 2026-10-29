// Package interaction owns interaction request lifetimes, not execution,
// authorization policy, transport, or presentation. Its queue is single-owner;
// producers communicate through the adapter's transport and buffered replies.
package interaction

import (
	"context"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/perm"
)

// Owner identifies one adapter/session generation. Implementations must be
// comparable (normally non-nil pointers), and Closed must be safe during cancellation.
// Use a nil interface, not a typed nil pointer, for an ownerless request.
// Replacing an owner fences requests from the preceding generation.
type Owner interface{ Closed() bool }

// Request owns one reply endpoint. Exactly one payload should be populated.
// Context preserves the producer's foreground or independent background lifetime.
type Request struct {
	ID             uint64
	Context        context.Context
	Owner          Owner
	Source         tool.AgentInteractionSource
	Permission     *agent.PermGateRequest
	PermissionView *perm.PermissionRequest
	Question       *Question
	Secret         *Secret
}

type Question struct {
	Request *tool.QuestionRequest
	Reply   chan *tool.QuestionResponse
}

// Secret is deliberately separate from tool questions and transcript records.
type Secret struct {
	Prompt string
	Reply  chan SecretResponse
}
type SecretResponse struct {
	Value     string
	Cancelled bool
}

func (r *Request) Valid(owner Owner) bool {
	return r != nil && (r.Context == nil || r.Context.Err() == nil) &&
		(r.Owner == nil || (r.Owner == owner && !r.Owner.Closed()))
}

// Cancel sends a best-effort negative response without blocking the owner.
// Reply endpoints are buffered and never closed by the lifecycle.
func (r *Request) Cancel() {
	if r == nil {
		return
	}
	switch {
	case r.Permission != nil:
		select {
		case r.Permission.Response <- agent.PermGateResponse{Reason: "interaction cancelled"}:
		default:
		}
	case r.Question != nil:
		select {
		case r.Question.Reply <- &tool.QuestionResponse{RequestID: r.Question.Request.ID, Cancelled: true}:
		default:
		}
	case r.Secret != nil:
		select {
		case r.Secret.Reply <- SecretResponse{Cancelled: true}:
		default:
		}
	}
}

// Queue preserves arrival order. The adapter retains the selected request while
// it presents it; Prune must also be applied to that request via Valid/Cancel.
// A slice permits read-only enumeration for task summaries without a second view.
type Queue []*Request

func (q *Queue) Enqueue(r *Request, owner Owner) bool {
	if !r.Valid(owner) {
		r.Cancel()
		return false
	}
	*q = append(*q, r)
	return true
}

// Prune cancels expired or foreign requests, not live background descendants.
func (q *Queue) Prune(owner Owner) bool {
	kept := (*q)[:0]
	for _, r := range *q {
		if r.Valid(owner) {
			kept = append(kept, r)
		} else {
			r.Cancel()
		}
	}
	changed := len(kept) != len(*q)
	clear((*q)[len(kept):])
	*q = kept
	return changed
}

func (q *Queue) Pop() *Request {
	if len(*q) == 0 {
		return nil
	}
	r := (*q)[0]
	(*q)[0] = nil
	*q = (*q)[1:]
	return r
}

func (q *Queue) Cancel() {
	for _, r := range *q {
		r.Cancel()
	}
	clear(*q)
	*q = nil
}
