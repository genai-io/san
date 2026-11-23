package app

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestACPMemoryLanguageReachesMain(t *testing.T) {
	for _, input := range []string{"remember: Prefer table tests.", "请忘记：优先使用表格测试。"} {
		t.Run(input, func(t *testing.T) {
			driver := aitest.New(aitest.Says("main response"))
			f := newACPFixture(t, driver, nil)
			id := f.session(t, t.TempDir())
			response, err := f.prompt(id, input)
			if err != nil || response.StopReason != protocol.StopReasonEndTurn {
				t.Fatalf("response=%+v err=%v", response, err)
			}
			messages := f.host.sessions[id].runtime.Messages()
			if driver.Calls() != 1 || len(messages) != 2 || messages[0].Text() != input || messages[1].Text() != "main response" {
				t.Fatalf("main calls=%d messages=%+v", driver.Calls(), messages)
			}
		})
	}
}

func TestACPConcurrentSessions(t *testing.T) {
	driver := aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		if len(req.Tools) != 0 {
			for _, schema := range req.Tools {
				t.Errorf("unexpected tool in no-tools turn: %s", schema.Name)
			}
		}
		if strings.Contains(req.System, "You select memories") {
			return aitest.Says(`{"selected_memories":["topic.md"]}`)(ctx, req)
		}
		return aitest.Says("done")(ctx, req)
	})
	f := newACPFixture(t, driver, nil)
	ids := []protocol.SessionID{f.session(t, t.TempDir()), f.session(t, t.TempDir())}
	for _, id := range ids {
		params := &f.host.sessions[id].runtimeBuildParams
		params.AutoMemory.Recall = setting.MemoryRecallConfig{Mode: setting.MemoryRecallWait, Timeout: 5 * time.Second}
		// configureAgents adds these even if they were absent from the initial catalog.
		for _, name := range []string{"Agent", "TaskOutput", "TaskStop"} {
			params.DisabledTools[name] = true
		}
		for _, schema := range params.Schemas() {
			params.DisabledTools[schema.Name] = true
		}
		dir := string(params.AutoMemory.Store)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "topic.md"), []byte("---\nname: Topic\ndescription: Session preference\ntype: project\n---\nPreference for "+string(id)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("- [Topic](topic.md)\n"), 0o600); err != nil {
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
			if strings.Contains(suffix, "memory-auto") || strings.Contains(suffix, string(s.runtimeBuildParams.AutoMemory.Store)) {
				t.Fatalf("legacy index or store path injected: %q", suffix)
			}
		}
	}
}
