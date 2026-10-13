package app

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/conv"
)

func scrollbackReadyMessage(cmd tea.Cmd) (scrollbackPrintReadyMsg, bool) {
	if cmd == nil {
		return scrollbackPrintReadyMsg{}, false
	}
	switch msg := cmd().(type) {
	case scrollbackPrintReadyMsg:
		return msg, true
	case tea.BatchMsg:
		for _, child := range msg {
			if ready, ok := scrollbackReadyMessage(child); ok {
				return ready, true
			}
		}
	}
	return scrollbackPrintReadyMsg{}, false
}

// Cancel at a chosen validity check, without sleeps or scheduler-dependent races.
type interactionCancellationContext struct {
	context.Context
	cancel            context.CancelFunc
	checksUntilCancel int
}

func (c *interactionCancellationContext) Err() error {
	if c.checksUntilCancel > 0 {
		c.checksUntilCancel--
		if c.checksUntilCancel == 0 {
			c.cancel()
		}
	}
	return c.Context.Err()
}

func TestCanceledInteractionResumesHistoryBeforeNextPrompt(t *testing.T) {
	for _, timing := range []string{"before-update", "during-update"} {
		for _, kind := range []string{"approval", "question", "secret"} {
			t.Run(kind+"/"+timing, func(t *testing.T) {
				m := interactionModel()
				defer m.closeInteractions()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				requestContext := &interactionCancellationContext{Context: ctx, cancel: cancel}
				active := interactionQuestion(m, requestContext, "canceled")
				switch kind {
				case "approval":
					active = interactionApproval(m, requestContext, "canceled")
				case "secret":
					active = &conv.InteractionRequest{
						ID: 1, Context: requestContext, Owner: m.conv.AgentToUI,
						Secret: &conv.SecretPromptRequestMsg{Prompt: "secret", Reply: make(chan conv.SecretPromptResponse, 1)},
					}
				}
				m.enqueueInteraction(active)
				if _, shown := m.activeOverlay(); !shown {
					t.Fatal("fixture did not display its active prompt")
				}

				print := m.flush.queueScrollbackPrint("HISTORY-ROW", 0)
				ready := print().(scrollbackPrintReadyMsg)
				m.Update(ready)
				if m.flush.pendingPrints[0].current != "" {
					t.Fatal("history print was not held behind the active prompt")
				}
				successor := interactionQuestion(m, context.Background(), "independent-worker")
				m.enqueueInteraction(successor)
				if timing == "before-update" {
					cancel()
				} else {
					// The first prune sees a live request; advanceInteractions sees
					// cancellation during the same Update and closes the modal.
					requestContext.checksUntilCancel = 2
				}
				_, commands := m.Update(conv.AgentToUITickMsg{})
				resumed, ok := scrollbackReadyMessage(commands)
				if !ok || resumed.id != ready.id {
					t.Fatal("canceling the active prompt did not restart its held history print")
				}
				if m.interactions.active != successor || m.interactions.shown {
					t.Fatal("successor lost its slot or appeared before history completed")
				}

				m.Update(resumed)
				if m.flush.pendingPrints[0].current != "HISTORY-ROW" {
					t.Fatal("resumed command did not prepare the original history chunk")
				}
				m.Update(scrollbackPrintDoneMsg{id: resumed.id})
				if len(m.flush.pendingPrints) != 0 || m.interactions.active != successor || !m.conv.Modal.Question.IsActive() {
					t.Fatal("independent worker did not receive its prompt after history drained")
				}
			})
		}
	}
}
