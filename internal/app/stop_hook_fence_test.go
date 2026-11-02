package app

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/setting"
)

// Run the real command with an engine-owned hook blocked until the test releases it.
func pendingStopOutcome(t *testing.T, m *model, blocked bool) func() stopHookResultMsg {
	t.Helper()
	data := setting.NewData()
	data.Hooks["Stop"] = []setting.Hook{{Hooks: []setting.HookCmd{{Type: "prompt", Prompt: "decide"}}}}
	m.services.Hook.SetSettings(data)
	entered, release := make(chan struct{}), make(chan struct{})
	m.services.Hook.SetLLMCompleter(func(ctx context.Context, _, _, _ string) (string, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		if blocked {
			return `{"continue":false,"reason":"old turn"}`, nil
		}
		return `{}`, nil
	}, "test")
	cmd := m.fireIdleHooksCmd(core.Result{StopReason: core.StopEndTurn})
	done := make(chan stopHookResultMsg, 1)
	go func() { done <- cmd().(stopHookResultMsg) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("hook did not start")
	}
	released := false
	t.Cleanup(func() {
		if !released {
			close(release)
		}
	})
	return func() stopHookResultMsg {
		close(release)
		released = true
		select {
		case msg := <-done:
			if msg.Blocked != blocked {
				t.Fatalf("hook blocked=%v, want %v", msg.Blocked, blocked)
			}
			return msg
		case <-time.After(3 * time.Second):
			t.Fatal("hook did not finish")
			return stopHookResultMsg{}
		}
	}
}

func TestDelayedStopOutcomeFence(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		for _, change := range []string{"prompt", "reset", "run-replaced", "session", "cancel", "escape", "ctrl-c", "next-turn", "queued-user", "nil-identity"} {
			t.Run(fmt.Sprintf("%s/blocked=%v", change, blocked), func(t *testing.T) {
				m, provider := compactQueueModel(t)
				finish := pendingStopOutcome(t, m, blocked)
				switch change {
				case "prompt":
					m.submit(pendingMessage{Message: core.UserMessage("new prompt", nil), FromUser: true})
					select {
					case <-provider.requests:
					case <-time.After(3 * time.Second):
						t.Fatal("successor inference did not start")
					}
					// Let producer recording finish without dispatching successor cleanup.
					deadline := time.After(3 * time.Second)
				waitSuccessor:
					for {
						select {
						case ev := <-m.AgentOutbox():
							if _, ok := ev.(core.TurnEnded); ok {
								break waitSuccessor
							}
						case <-deadline:
							t.Fatal("successor did not finish")
						}
					}
				case "reset":
					if err := m.ResetAgentRuntime(); err != nil {
						t.Fatal(err)
					}
				case "run-replaced":
					if err := m.services.Agent.Reset(t.Context()); err != nil {
						t.Fatal(err)
					}
				case "session":
					old := m.services.Session
					if err := old.Close(); err != nil {
						t.Fatal(err)
					}
					replacement, _, err := m.services.SessionStore.Open("s")
					if err != nil {
						t.Fatal(err)
					}
					m.services.Session = replacement
					t.Cleanup(func() { _ = old.Close() })
				case "cancel":
					m.handleStreamCancel()
				case "escape":
					m.routeKeypress(tea.KeyPressMsg{Code: tea.KeyEscape})
				case "ctrl-c":
					m.routeKeypress(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
				case "next-turn":
					m.OnTurnEnd(core.Result{StopReason: core.StopEndTurn})
				case "queued-user":
					m.userInput.Queue.Enqueue("explicit user input", nil)
				}
				msg := finish()
				if change == "nil-identity" {
					msg.scope = nil
				}
				current := m.pendingStopHook
				beforeRows, beforePending, beforeQueue := len(m.conv.Messages), m.pendingMessages.Len(), m.userInput.Queue.Len()
				before, err := os.ReadFile(m.services.Session.TranscriptPath())
				if err != nil {
					t.Fatal(err)
				}
				_, cmd := m.dispatch(msg)
				after, err := os.ReadFile(m.services.Session.TranscriptPath())
				if err != nil {
					t.Fatal(err)
				}
				if cmd != nil || len(m.conv.Messages) != beforeRows || m.pendingMessages.Len() != beforePending || m.userInput.Queue.Len() != beforeQueue || string(before) != string(after) {
					t.Fatalf("stale result changed state: cmd=%v rows=%d->%d pending=%d->%d queue=%d->%d\ntranscript before (%d bytes):\n%s\ntranscript after (%d bytes):\n%s",
						cmd != nil, beforeRows, len(m.conv.Messages), beforePending, m.pendingMessages.Len(), beforeQueue, m.userInput.Queue.Len(), len(before), before, len(after), after)
				}
				if (change == "next-turn" || change == "nil-identity") && m.pendingStopHook != current {
					t.Fatal("old result cleared current decision")
				}
			})
		}
	}
}

func TestPendingStopHookKeyPrecedence(t *testing.T) {
	for _, scenario := range []string{"mission-offer", "ghost-suggestion", "plain-escape", "ctrl-c-draft"} {
		t.Run(scenario, func(t *testing.T) {
			m, _ := compactQueueModel(t)
			finish := pendingStopOutcome(t, m, true)
			pending := m.pendingStopHook
			key := tea.KeyPressMsg{Code: tea.KeyEscape}
			switch scenario {
			case "mission-offer":
				m.env.SessionPermissions.SetMode(setting.ModeAutoPilot)
				m.env.AutoPilot.Mission = "run the tests"
				if !m.missionOfferVisible() {
					t.Fatal("mission offer must be visible before Escape")
				}
			case "ghost-suggestion":
				m.userInput.PromptSuggestion.Text = "run the tests"
			case "ctrl-c-draft":
				key = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
				m.userInput.Textarea.SetValue("draft")
				m.userInput.LastCtrlC = time.Now()
			}
			if m.conv.Stream.Active || m.pendingMessages.Held() || pending == nil {
				t.Fatal("expected an idle pending hook without an input hold")
			}
			cmd, handled := m.routeKeypress(key)
			if !handled {
				t.Fatal("key was not handled")
			}
			if scenario != "plain-escape" {
				if cmd != nil || m.pendingStopHook != pending || m.pendingMessages.Held() {
					t.Fatal("first key cancelled the hook instead of dismissing composer content")
				}
				if m.missionOfferVisible() || m.userInput.PromptSuggestion.Text != "" || m.userInput.Textarea.Value() != "" {
					t.Fatal("first key did not dismiss composer content")
				}
				if scenario == "ctrl-c-draft" && !m.userInput.LastCtrlC.IsZero() {
					t.Fatal("clearing the draft must disarm exit")
				}
				if _, handled := m.routeKeypress(key); !handled {
					t.Fatal("second key was not handled")
				}
			}
			if m.pendingStopHook != nil || !m.pendingMessages.Held() {
				t.Fatal("key without composer content must cancel the pending hook and hold input")
			}
			msg := finish()
			before := len(m.conv.Messages)
			if _, cmd := m.dispatch(msg); cmd != nil || len(m.conv.Messages) != before {
				t.Fatal("cancelled hook outcome was applied")
			}
		})
	}
}

func TestLiveStopOutcomeConsumedOnce(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(fmt.Sprint(blocked), func(t *testing.T) {
			m, _ := compactQueueModel(t)
			msg := pendingStopOutcome(t, m, blocked)()
			beforeRows := len(m.conv.Messages)
			before, err := os.ReadFile(m.services.Session.TranscriptPath())
			if err != nil {
				t.Fatal(err)
			}
			m.dispatch(msg)
			after, err := os.ReadFile(m.services.Session.TranscriptPath())
			if err != nil {
				t.Fatal(err)
			}
			if m.pendingStopHook != nil {
				t.Fatal("live outcome not consumed")
			}
			if blocked && len(m.conv.Messages) != beforeRows+1 {
				t.Fatal("live block did not inject continuation")
			}
			if !blocked && string(before) == string(after) {
				t.Fatal("live success did not persist turn state")
			}
			rows := len(m.conv.Messages)
			_, cmd := m.dispatch(msg)
			again, err := os.ReadFile(m.services.Session.TranscriptPath())
			if err != nil {
				t.Fatal(err)
			}
			if cmd != nil || len(m.conv.Messages) != rows || (!blocked && string(after) != string(again)) {
				t.Fatal("duplicate outcome applied")
			}
		})
	}
}
