package app

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/agent"
	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type finalizationProvider struct {
	restartStubProvider
	client *ai.Client
}

func (p *finalizationProvider) Client(string, map[string]string) (*ai.Client, error) {
	return p.client, nil
}

// Execute actual Tea commands, including the asynchronous receipt wait. Render
// messages need no terminal in this test; only the completion returns to Update.
func runFinalizationCommands(cmd tea.Cmd, completed chan<- turnFinalizedMsg) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, child := range msg {
			go runFinalizationCommands(child, completed)
		}
	case turnFinalizedMsg:
		completed <- msg
	}
}

func TestCancelledTurnFinalizationConsumerAndSession(t *testing.T) {
	for _, mode := range []string{"saved", "natural", "essential-write-failed", "write-failed", "session-replaced", "outbox-replaced"} {
		t.Run(mode, func(t *testing.T) {
			m, store := resumedModel(t, []core.Message{core.UserMessage("history", nil)})
			llm := aitest.Always(aitest.Hangs())
			if mode == "natural" {
				llm = aitest.Always(aitest.Says("done"))
			}
			naturalEnd := make(chan struct{})
			opened := m.services.Session
			m.agentSession = opened
			p := &finalizationProvider{client: llm.Client()}
			if err := m.services.Agent.Start(agent.BuildParams{Provider: p, ModelID: "m", OnEvent: func(ev core.Event) {
				recordAgentEvent(opened, ev)
				if _, ok := ev.(core.TurnEnded); ok && mode == "natural" {
					close(naturalEnd)
				}
			}}, m.services.Agent.Messages()); err != nil {
				t.Fatal(err)
			}
			if err := m.services.Agent.Send(core.UserMessage("work", nil)); err != nil {
				t.Fatal(err)
			}
			deadline := time.After(2 * time.Second)
			for llm.Calls() == 0 {
				select {
				case <-deadline:
					t.Fatal("inference not started")
				default:
					time.Sleep(time.Millisecond)
				}
			}
			if mode == "essential-write-failed" {
				// A malformed essential message exercises the real recorder/store
				// failure path, not a synthetic receipt.RecordError call.
				recordAgentEvent(opened, sdkagent.MessageAdded{Message: ai.UserMessage("missing ID")})
			}
			if mode == "natural" {
				select {
				case <-naturalEnd:
				case <-deadline:
					t.Fatal("natural completion missing")
				}
			}
			// Queue BEFORE cancellation; executing the returned commands must not
			// release it or install a successor ahead of terminal cleanup.
			m.userInput.Queue.Enqueue("held until later input", nil)
			cmd := m.handleStreamCancel()
			if cmd == nil {
				t.Fatal("cancel did not schedule finalization wait")
			}
			completed := make(chan turnFinalizedMsg, 1)
			go runFinalizationCommands(cmd, completed)
			select {
			case msg := <-completed:
				t.Fatalf("slow consumer falsely completed: %v", msg.err)
			case <-time.After(300 * time.Millisecond):
			}
			if m.userInput.Queue.Len() != 1 || llm.Calls() != 1 {
				t.Fatal("cancel released queued input before terminal cleanup")
			}
			receipt := m.services.Agent.CancelTurn()
			if receipt == nil {
				t.Fatal("missing receipt")
			}
			outbox := m.services.Agent.Outbox()
			var end core.TurnEnded
		drain:
			for {
				select {
				case ev := <-outbox:
					if e, ok := ev.(core.TurnEnded); ok {
						end = e
						break drain
					}
				case <-deadline:
					t.Fatal("missing terminal")
				}
			}
			short, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			if err := receipt.Wait(short); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("consumer not handled: %v", err)
			}
			if mode == "write-failed" || mode == "session-replaced" {
				if err := opened.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "session-replaced" {
				replacement, err := store.Create(session.CreateOptions{ID: "replacement"})
				if err != nil {
					t.Fatal(err)
				}
				m.services.Session = replacement
			}
			if mode == "outbox-replaced" {
				outbox = make(chan core.Event)
			}
			if mode == "essential-write-failed" {
				if opened.Err() == nil {
					t.Fatal("essential recorder failure not sticky")
				}
				// Isolate the producer wrapper: a successful consumer ack cannot
				// hide the recorder error, even without appendTurnState's error.
				receipt.Acknowledge(nil)
				if err := receipt.Wait(context.Background()); !errors.Is(err, opened.Err()) {
					t.Fatalf("production observer lost essential error: %v", err)
				}
			}
			if mode == "saved" {
				m.services.Agent.SetPluginRoot("old-plugin")
				m.skillUsedThisTurn = true
				m.pendingPluginRoot = "next-plugin"
				m.sendToAgent(pendingMessage{Message: core.UserMessage("explicit continuation", nil), fromUser: true})
				if len(m.pendingMessages) != 1 || llm.Calls() != 1 {
					t.Fatal("explicit continuation crossed cleanup fence")
				}
			}
			// The normal single consumer owns cleanup and acknowledges only after the
			// state append. No returned render/listener commands need to run to settle.
			conv.Update(m, &m.conv, conv.AgentOutboxMsg{Outbox: outbox, Event: end})
			ctx, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			err := receipt.Wait(ctx)
			if (err != nil) != (mode != "saved" && mode != "natural") {
				t.Fatalf("finalization error = %v, mode=%s", err, mode)
			}
			select {
			case msg := <-completed:
				m.dispatch(msg)
			case <-ctx.Done():
				t.Fatal("async command did not complete after handling")
			}
			if m.cancellingTurn != nil {
				t.Fatal("submission fence not released")
			}
			if m.userInput.Queue.Len() != 1 {
				t.Fatal("terminal finalization released held queued input")
			}
			if mode != "saved" && !m.backgroundNoticesHeld {
				t.Fatal("cancellation released background hold")
			}
			if mode == "saved" {
				if m.skillUsedThisTurn || m.services.Agent.PluginRoot() != "next-plugin" {
					t.Fatal("old cleanup overwrote successor scope")
				}
				until := time.Now().Add(time.Second)
				for llm.Calls() < 2 && time.Now().Before(until) {
					time.Sleep(time.Millisecond)
				}
				if llm.Calls() != 2 {
					t.Fatal("explicit continuation did not start")
				}
			}
		})
	}
}
