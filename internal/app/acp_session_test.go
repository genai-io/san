package app

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

const memoryMaintenancePrompt = "remember: Prefer table tests."
const memoryMaintenanceProposal = `{"action":"remember","file":"tests.md","name":"Tests","description":"Test style","type":"feedback","body":"Prefer table tests."}`
const memoryMaintenanceNotice = "Memory · saved tests.md to project"

func TestACPShowsMaintenanceNoticeWithoutMainInference(t *testing.T) {
	driver := aitest.New(aitest.Says(memoryMaintenanceProposal))
	f := newACPFixture(t, driver, nil)
	id := f.session(t, t.TempDir())
	response, err := f.prompt(id, memoryMaintenancePrompt)
	if err != nil || response.StopReason != protocol.StopReasonEndTurn {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	requests := driver.Sent()
	if len(requests) != 1 || len(requests[0].Tools) != 0 {
		t.Fatalf("expected one tool-free maintenance request: %+v", requests)
	}
	if s := f.host.sessions[id]; s.runtime.Active() || len(s.runtime.Messages()) != 0 {
		t.Fatal("maintenance started the main runtime")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, notification := range f.updates {
		if chunk, ok := notification.Update.Value.(protocol.SessionUpdateAgentMessageChunkVariant); ok {
			if text, ok := chunk.Content.Value.(protocol.ContentBlockTextVariant); ok && text.Text == memoryMaintenanceNotice {
				return
			}
		}
	}
	t.Fatalf("missing maintenance notice in %+v", f.updates)
}

func TestACPConcurrentSessions(t *testing.T) {
	driver := aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		if len(req.Tools) != 0 {
			for _, schema := range req.Tools {
				t.Errorf("unexpected tool in no-tools turn: %s", schema.Name)
			}
		}
		if strings.Contains(req.System, "You select memories") {
			return aitest.Says(`{"selected_memories":["project/topic.md"]}`)(ctx, req)
		}
		return aitest.Says("done")(ctx, req)
	})
	f := newACPFixture(t, driver, nil)
	ids := []protocol.SessionID{f.session(t, t.TempDir()), f.session(t, t.TempDir())}
	for _, id := range ids {
		params := &f.host.sessions[id].runtimeBuildParams
		// configureAgents adds these even if they were absent from the initial catalog.
		for _, name := range []string{"Agent", "TaskOutput", "TaskStop"} {
			params.DisabledTools[name] = true
		}
		for _, schema := range params.Schemas() {
			params.DisabledTools[schema.Name] = true
		}
		dir := params.AutoMemory.Project
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "topic.md"), []byte("---\nname: Topic\ndescription: Session preference\ntype: project\n---\nPreference for "+string(id)), 0o600); err != nil {
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
			if hasRecall := strings.Contains(suffix, "Preference for "+string(id)); hasRecall != (i == 0) {
				t.Fatalf("recall must appear once in active history: %q", messages[i].Text())
			}
			if strings.Contains(suffix, "memory-auto") || strings.Contains(suffix, s.runtimeBuildParams.AutoMemory.Project) {
				t.Fatalf("legacy index or store path injected: %q", suffix)
			}
		}
	}
}
