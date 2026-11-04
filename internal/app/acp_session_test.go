package app

import (
	"sync"
	"testing"

	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestACPConcurrentSessions(t *testing.T) {
	f := newACPFixture(t, aitest.Always(aitest.Says("done")), nil)
	ids := []protocol.SessionID{f.session(t, t.TempDir()), f.session(t, t.TempDir())}
	var workers sync.WaitGroup
	for _, id := range ids {
		workers.Go(func() {
			for range 4 {
				if _, err := f.prompt(id, string(id)); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	workers.Wait()
	for _, id := range ids {
		s := f.host.sessions[id]
		messages := s.runtime.Messages()
		if len(messages) != 8 {
			t.Fatalf("messages = %d, want 8", len(messages))
		}
		for i := 0; i < len(messages); i += 2 {
			if messages[i].Text() != string(id) {
				t.Fatal("cross-session input")
			}
		}
	}
}
