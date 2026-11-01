package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
)

func TestQueueEnterWithHeldHookAfterCancellation(t *testing.T) {
	for _, mode := range []string{"head", "nonhead", "no-enter", "direct"} {
		t.Run(mode, func(t *testing.T) {
			m, provider := compactQueueModel(t)
			if err := m.services.Agent.Send(core.UserMessage("initial work", nil)); err != nil {
				t.Fatal(err)
			}
			select {
			case <-provider.requests:
			case <-time.After(time.Second):
				t.Fatal("initial inference did not start")
			}
			completed := make(chan turnFinalizedMsg, 1)
			go runFinalizationCommands(m.handleStreamCancel(), completed)
			if m.cancellingTurn == nil {
				t.Fatal("cancel did not capture finalization")
			}
			m.handleStopHookResult(stopHookResultMsg{Blocked: true, Reason: "held hook"})
			m.userInput.Queue.Enqueue("queued head", nil)
			m.userInput.Queue.Enqueue("queued successor", nil)
			m.sendPendingMessages()
			m.sendNextQueuedInputIfIdle()
			if len(m.pendingMessages) != 1 || m.pendingMessages[0].fromUser || !m.backgroundNoticesHeld {
				t.Fatal("hook did not remain held")
			}
			select {
			case <-provider.requests:
				t.Fatal("held hook woke agent before Enter")
			default:
			}
			if mode != "no-enter" {
				m.routeKeypress(tea.KeyPressMsg{Code: tea.KeyUp})
				if mode != "nonhead" {
					m.routeKeypress(tea.KeyPressMsg{Code: tea.KeyUp})
				}
				m.routeKeypress(tea.KeyPressMsg{Code: tea.KeyEnter})
			}
			if mode == "direct" {
				m.sendToAgent(pendingMessage{Message: core.UserMessage("direct continuation", nil), fromUser: true})
			}
			if m.userInput.Queue.Len() != 2 {
				t.Fatal("queue crossed finalization fence")
			}
			outbox := m.services.Agent.Outbox()
			deadline := time.After(2 * time.Second)
		drain:
			for {
				select {
				case ev := <-outbox:
					if end, ok := ev.(core.TurnEnded); ok {
						conv.Update(m, &m.conv, conv.AgentOutboxMsg{Outbox: outbox, Event: end})
						break drain
					}
				case <-deadline:
					t.Fatal("terminal event missing")
				}
			}
			var completion turnFinalizedMsg
			select {
			case completion = <-completed:
				m.dispatch(completion)
			case <-deadline:
				t.Fatal("acknowledged finalization did not complete")
			}
			if mode == "no-enter" {
				if !m.backgroundNoticesHeld || len(m.pendingMessages) != 1 || m.userInput.Queue.Len() != 2 {
					t.Fatal("finalization released held work without Enter")
				}
				select {
				case <-provider.requests:
					t.Fatal("held hook independently woke agent")
				case <-time.After(20 * time.Millisecond):
				}
				return
			}
			want, remaining := "queued head", 1
			if mode == "direct" {
				want, remaining = "direct continuation", 2
			}
			for {
				select {
				case request := <-provider.requests:
					count := 0
					for _, message := range request {
						if strings.Contains(message.Text(), want) {
							count++
						}
					}
					if count == 0 {
						continue
					}
					if count != 1 || m.backgroundNoticesHeld || len(m.pendingMessages) != 0 || m.userInput.Queue.Len() != remaining {
						t.Fatal("continuation not sent exactly once through user submit")
					}
					m.dispatch(completion)
					if m.userInput.Queue.Len() != remaining {
						t.Fatal("completion replay released successor")
					}
					return
				case <-deadline:
					t.Fatal("explicit continuation lost behind held hook")
				}
			}
		})
	}
}

func TestQueueEnterContinuesCancelledTurn(t *testing.T) {
	for _, mode := range []string{"after", "during", "escape", "delete", "edit", "reenter", "stale"} {
		t.Run(mode, func(t *testing.T) {
			m, provider := compactQueueModel(t)
			m.backgroundNoticesHeld = true
			receipt := &core.TurnFinalization{}
			if mode != "after" {
				m.cancellingTurn = receipt
			}
			m.userInput.Queue.Enqueue("resume queued work", nil)
			m.userInput.Queue.Enqueue("unrelated successor", nil)
			m.userInput.Textarea.SetValue("draft")
			m.routeKeypress(tea.KeyPressMsg{Code: tea.KeyUp})
			m.routeKeypress(tea.KeyPressMsg{Code: tea.KeyUp})
			key := tea.KeyEnter
			if mode == "escape" {
				key = tea.KeyEscape
			}
			m.routeKeypress(tea.KeyPressMsg{Code: key})
			if mode != "after" {
				if m.userInput.Queue.Len() != 2 || !m.backgroundNoticesHeld {
					t.Fatal("Enter crossed finalization fence")
				}
				m.sendNextQueuedInputIfIdle()
				if mode == "delete" {
					m.userInput.Queue.Dequeue()
				}
				if mode == "edit" {
					m.userInput.Queue.UpdateAt(0, "changed", nil)
				}
				if mode == "reenter" {
					m.routeKeypress(tea.KeyPressMsg{Code: tea.KeyUp})
					m.routeKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
				}
				if mode == "stale" {
					m.cancellingTurn = &core.TurnFinalization{}
				}
				m.dispatch(turnFinalizedMsg{receipt: receipt, outbox: m.services.Agent.Outbox()})
			}
			wantSend := mode == "after" || mode == "during"
			if wantSend {
				select {
				case request := <-provider.requests:
					if !strings.Contains(request[len(request)-1].Text(), "resume queued work") {
						t.Fatal("wrong input sent")
					}
				case <-time.After(time.Second):
					t.Fatal("explicit Enter did not resume")
				}
				if m.userInput.Queue.Len() != 1 || m.userInput.Textarea.Value() != "draft" {
					t.Fatal("duplicate submission or lost draft")
				}
				m.dispatch(turnFinalizedMsg{receipt: receipt, outbox: m.services.Agent.Outbox()})
				if m.userInput.Queue.Len() != 1 {
					t.Fatal("receipt replay released successor")
				}
			} else {
				m.sendNextQueuedInputIfIdle()
				if !m.backgroundNoticesHeld {
					t.Fatal("automatic path released hold")
				}
				select {
				case <-provider.requests:
					t.Fatal("unrequested inference")
				default:
				}
			}
		})
	}
}
