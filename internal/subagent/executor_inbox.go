package subagent

import (
	"context"
	"sync"

	"github.com/genai-io/san/internal/broker"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/sdk-go/pkg/ai"
)

// runWithInbox closes message acceptance and drains late input within one execution budget.
func runWithInbox(ctx context.Context, ag core.Agent, taskID string, maxSteps int) (*core.Result, error) {
	// Delivery and the final pending check share a lock: after closing,
	// even a broker callback already looked up must reject instead of dropping input.
	var mailMu sync.Mutex
	accepting := true
	if taskID != "" {
		broker.Register(taskID, func(m broker.Message) bool {
			mailMu.Lock()
			defer mailMu.Unlock()
			if !accepting || ctx.Err() != nil {
				return false
			}
			ag.Append(core.UserMessage(m.Content, nil))
			return true
		})
		defer broker.Unregister(taskID)
	}
	result, err := ag.ThinkAct(ctx)
	mailMu.Lock()
	for {
		unread := ag.Pending() > 0
		if !unread {
			messages := ag.Messages()
			unread = len(messages) > 0 && messages[len(messages)-1].Role == ai.RoleUser
		}
		if !unread {
			break
		}
		if result != nil && result.StopReason == core.StopEndTurn && maxSteps > 0 && result.Steps >= maxSteps {
			result.StopReason = core.StopMaxSteps
		}
		if err != nil || ctx.Err() != nil || result == nil || result.StopReason != core.StopEndTurn {
			accepting = false
			mailMu.Unlock()
			// A canceled exchange settles accepted input into native history without inference.
			if ag.Pending() > 0 {
				flushCtx, cancel := context.WithCancel(ctx)
				cancel()
				_, _ = ag.ThinkAct(flushCtx)
			}
			if result != nil {
				result.Messages = ag.Messages()
			}
			return result, err
		}
		if maxSteps > 0 {
			ag.SetMaxSteps(maxSteps - result.Steps)
		}
		mailMu.Unlock()
		next, nextErr := ag.ThinkAct(ctx)
		if next != nil && result != nil {
			next.Steps += result.Steps
			next.ToolUses += result.ToolUses
			next.Usage.Add(result.Usage)
		}
		result, err = next, nextErr
		mailMu.Lock()
	}
	accepting = false
	mailMu.Unlock()
	return result, err
}
