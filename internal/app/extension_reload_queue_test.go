package app

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func reloadQueueModel(t *testing.T) *model {
	t.Helper()
	m, _ := resumedModel(t, nil)
	e := extensionReloadModel(t, m.env.CWD)
	m.services.Skill, m.services.Command = e.services.Skill, e.services.Command
	m.services.Subagent, m.services.MCP = e.services.Subagent, e.services.MCP
	m.services.Plugin, m.services.Persona = e.services.Plugin, e.services.Persona
	m.services.Tool = tool.Default()
	m.learnedStores, m.userInput = e.learnedStores, e.userInput
	m.env.LLMProvider = acpTestProvider{aitest.Always(aitest.Says("done")).Client()}
	if _, err := m.ensureAgentRuntime(); err != nil {
		t.Fatal(err)
	}
	return m
}

// Run returned UI commands, including batches, without blocking on listeners.
func reloadQueueCommands(cmd tea.Cmd, messages chan<- tea.Msg) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			go reloadQueueCommands(child, messages)
		}
	} else {
		messages <- msg
	}
}

func TestExtensionReloadQueuedTurnBoundary(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "normal"
		if cancelled {
			name = "cancelled-pending"
		}
		t.Run(name, func(t *testing.T) {
			m := reloadQueueModel(t)
			owner, tools, outbox := m.services.Session, m.services.Agent.Tools(), m.AgentOutbox()
			m.sendToAgent(pendingMessage{Message: core.UserMessage("original", nil), FromUser: true})
			// Observe the actual original terminal event; leave its consumer receipt
			// outstanding while reload and queue release run through the UI path.
			var end core.TurnEnded
			deadline := time.After(5 * time.Second)
		terminal:
			for {
				select {
				case ev := <-outbox:
					if v, ok := ev.(core.TurnEnded); ok {
						end = v
						break terminal
					}
				case <-deadline:
					t.Fatal("original turn did not finish")
				}
			}
			if err := m.ReloadAfterPluginChange(); err != nil {
				t.Fatal(err)
			}
			var cmd tea.Cmd
			if cancelled {
				m.handleStreamCancel()
				if m.pendingMessages.Cancelling() != end.Result.Finalization {
					t.Fatal("cancel did not retain the original turn receipt")
				}
				m.sendToAgent(pendingMessage{Message: core.UserMessage("queued successor", nil), FromUser: true})
				conv.Update(m, &m.conv, conv.AgentOutboxMsg{Outbox: outbox, Event: end})
				// Finalization Update, not ensureAgentRuntime, releases held pending input.
				err := end.Result.Finalization.Wait(t.Context())
				if err != nil && err != core.ErrTurnCompleted {
					t.Fatal(err)
				}
				_, cmd = m.Update(turnFinalizedMsg{receipt: end.Result.Finalization, outbox: outbox, err: err})
			} else {
				m.userInput.Queue.Enqueue("queued successor", nil)
				cmd, _ = conv.Update(m, &m.conv, conv.AgentOutboxMsg{Outbox: outbox, Event: end})
			}
			if m.pendingMessages.Len() != 1 || m.services.Agent.Tools() != tools {
				t.Fatal("successor admitted before receipt-safe UI handoff")
			}
			messages := make(chan tea.Msg, 100)
			go reloadQueueCommands(cmd, messages)
			var ready pendingInputReadyMsg
		handoff:
			for {
				select {
				case msg := <-messages:
					if v, ok := msg.(pendingInputReadyMsg); ok {
						ready = v
						break handoff
					}
				case <-deadline:
					t.Fatal("missing reload handoff")
				}
			}
			if m.pendingMessages.First().Text() != "queued successor" {
				t.Fatal("queued content changed")
			}
			m.Update(ready)
			if m.services.Agent.Tools() == tools || m.agentReloadPending || m.pendingMessages.Len() != 0 {
				t.Fatal("automatic successor admission did not rebuild")
			}
			if m.services.Session != owner || m.AgentOutbox() == outbox {
				t.Fatal("wrong recording owner or outbox")
			}
			// A duplicate handoff from the old source cannot affect the replacement.
			if _, cmd := m.Update(ready); cmd != nil {
				t.Fatal("duplicate handoff was not fenced")
			}
		})
	}
}

func TestExtensionReloadCurrentTurnInjection(t *testing.T) {
	m := reloadQueueModel(t)
	// Keep a real original turn open while injecting, rather than merely
	// setting the UI's stream flag on an idle runtime.
	m.env.LLMProvider = acpTestProvider{aitest.Always(aitest.Hangs()).Client()}
	if err := m.StopAgentRuntime(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ensureAgentRuntime(); err != nil {
		t.Fatal(err)
	}
	m.sendToAgent(pendingMessage{Message: core.UserMessage("original running turn", nil), FromUser: true})
	select {
	case <-m.AgentOutbox():
	case <-time.After(5 * time.Second):
		t.Fatal("original turn did not start")
	}
	tools := m.services.Agent.Tools()
	if err := m.ReloadAfterPluginChange(); err != nil {
		t.Fatal(err)
	}
	cmd := m.sendToAgent(pendingMessage{Message: core.UserMessage("injected into current turn", nil), FromUser: true})
	if cmd != nil || m.pendingMessages.Len() != 0 || m.services.Agent.Tools() != tools || !m.agentReloadPending {
		t.Fatal("current-turn injection rebuilt or deferred delivery")
	}
}
