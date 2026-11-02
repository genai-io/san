package app

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/app/conv"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/setting"
)

func attachTurnSession(t *testing.T, m *model) {
	t.Helper()
	store, err := session.NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opened := mustCreateSession(t, store, session.CreateOptions{ID: "turn", AgentID: "main"})
	m.services.Session = opened
	m.env.SessionPermissions = setting.NewSessionPermissions()
	t.Cleanup(func() { _ = opened.Close() })
}

func TestNormalTurnRecordsBeforeQueuedSuccessor(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved", true: "write-failed"}[failed], func(t *testing.T) {
			m, provider := compactQueueModel(t)
			m.submit(pendingMessage{Message: core.UserMessage("first", nil), FromUser: true})
			select {
			case <-provider.requests:
			case <-time.After(3 * time.Second):
				t.Fatal("first inference missing")
			}
			var end core.TurnEnded
			deadline := time.After(3 * time.Second)
		drain:
			for {
				select {
				case ev := <-m.AgentOutbox():
					if e, ok := ev.(core.TurnEnded); ok {
						end = e
						break drain
					}
					conv.Update(m, &m.conv, conv.AgentOutboxMsg{Outbox: m.AgentOutbox(), Event: ev})
				case <-deadline:
					t.Fatal("turn end missing")
				}
			}
			// Observing TurnEnded ensures producer recording finished before inspection.
			m.userInput.Queue.Enqueue("successor", nil)
			m.env.SessionName = "completed state"
			if failed {
				if err := m.services.Session.Close(); err != nil {
					t.Fatal(err)
				}
			}
			conv.Update(m, &m.conv, conv.AgentOutboxMsg{Outbox: m.AgentOutbox(), Event: end})
			if failed {
				if m.userInput.Queue.Len() != 1 || m.pendingStopHook != nil {
					t.Fatal("record failure admitted a successor or idle hooks")
				}
				select {
				case <-provider.requests:
					t.Fatal("successor inference crossed failed recording")
				default:
				}
				return
			}
			select {
			case <-provider.requests:
			case <-time.After(3 * time.Second):
				t.Fatal("successor missing")
			}
			deadline = time.After(3 * time.Second)
		waitSuccessor:
			for {
				select {
				case ev := <-m.AgentOutbox():
					if _, ok := ev.(core.TurnEnded); ok {
						break waitSuccessor
					}
				case <-deadline:
					t.Fatal("successor end missing")
				}
			}
			data, err := os.ReadFile(m.services.Session.TranscriptPath())
			if err != nil {
				t.Fatal(err)
			}
			stateAt := strings.Index(string(data), "completed state")
			successorAt := strings.Index(string(data), "successor")
			if stateAt < 0 || successorAt <= stateAt {
				t.Fatalf("state not recorded before successor: state=%d successor=%d", stateAt, successorAt)
			}
			if m.pendingStopHook != nil {
				t.Fatal("queued successor also scheduled idle hooks")
			}
		})
	}
}

func TestStopContinuationRequiresRecording(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved", true: "write-failed"}[failed], func(t *testing.T) {
			m, provider := compactQueueModel(t)
			finish := pendingStopOutcome(t, m, true)
			m.env.SessionName = "post-hook state"
			if failed {
				if err := m.services.Session.Close(); err != nil {
					t.Fatal(err)
				}
			}
			before := len(m.conv.Messages)
			m.dispatch(finish())
			if len(m.conv.Messages) != before+1 || m.pendingStopHook != nil {
				t.Fatal("missing continuation/save failure notice or pending decision not consumed")
			}
			if failed {
				for _, msg := range m.conv.Messages {
					if strings.HasPrefix(msg.Content, "Stop hook blocked:") {
						t.Fatal("hook continuation admitted after failed recording")
					}
				}
				select {
				case <-provider.requests:
					t.Fatal("hook continuation started inference")
				default:
				}
				return
			}
			deadline := time.After(3 * time.Second)
			for {
				select {
				case ev := <-m.AgentOutbox():
					if _, ok := ev.(core.TurnEnded); !ok {
						continue
					}
					data, err := os.ReadFile(m.services.Session.TranscriptPath())
					if err != nil {
						t.Fatal(err)
					}
					stateAt := strings.Index(string(data), "post-hook state")
					continuationAt := strings.Index(string(data), "Stop hook blocked:")
					if stateAt < 0 || continuationAt <= stateAt {
						t.Fatalf("state not recorded before hook continuation: state=%d continuation=%d", stateAt, continuationAt)
					}
					return
				case <-deadline:
					t.Fatal("hook continuation did not finish")
				}
			}
		})
	}
}
