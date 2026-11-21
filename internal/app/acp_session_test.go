package app

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestACPConcurrentSessions(t *testing.T) {
	f := newACPFixture(t, aitest.Always(aitest.Says("done")), nil)
	ids := []protocol.SessionID{f.session(t, t.TempDir()), f.session(t, t.TempDir())}
	for _, id := range ids {
		dir := f.host.sessions[id].runtimeBuildParams.AutoMemory.Project
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("- Index for "+string(id)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
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
			input, suffix, _ := strings.Cut(messages[i].Text(), "\n\n")
			if input != string(id) {
				t.Fatal("cross-session input")
			}
			if hasIndex := strings.Contains(suffix, "Index for "+string(id)); hasIndex != (i == 0) {
				t.Fatal("auto memory index must be prepared once per session")
			}
		}
	}
}
