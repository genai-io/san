package subagent_test

import (
	"context"
	"encoding/json"
	"iter"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/tests/integration/testutil"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestBackgroundPermissionSnapshotSurvivesParentModeChange(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		initial, later, want subagent.PermissionMode
		named                bool
		wantWrite            bool
	}{
		{"read-only parent becomes YOLO", subagent.PermissionExplore, subagent.PermissionBypass, subagent.PermissionExplore, false, false},
		{"YOLO parent becomes read-only", subagent.PermissionBypass, subagent.PermissionExplore, subagent.PermissionBypass, false, true},
		{"named read-only definition stays independent", subagent.PermissionBypass, subagent.PermissionDefault, subagent.PermissionExplore, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := task.Initialize(task.Options{OutputDir: t.TempDir()}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(task.ResetDefaultTracker)
			cwd := t.TempDir()
			path := filepath.Join(cwd, "result.txt")
			input, err := json.Marshal(map[string]string{"file_path": path, "content": "written"})
			if err != nil {
				t.Fatal(err)
			}
			started, release := make(chan struct{}), make(chan struct{})
			first := func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
				return func(yield func(ai.Delta, error) bool) {
					close(started)
					select {
					case <-ctx.Done():
						yield(ai.Delta{}, ctx.Err())
						return
					case <-release:
					}
					for delta, err := range aitest.Asks(ai.ToolCall{ID: "write", Name: "Write", Input: string(input)})(ctx, req) {
						if !yield(delta, err) {
							return
						}
					}
				}
			}
			provider := driverProvider{&testutil.FakeProvider{}, aitest.New(first, aitest.Says("done"))}
			executor := subagent.NewExecutor(provider, cwd, "fake-model", nil)
			var current atomic.Value
			current.Store(tc.initial)
			executor.SetParentPermissionMode(func() subagent.PermissionMode { return current.Load().(subagent.PermissionMode) })
			req := tool.AgentExecRequest{Prompt: "Write the result"}
			if tc.named {
				req.Agent = "custom"
				req.ResolvedAgentConfig = &subagent.AgentConfig{Name: "custom", PermissionMode: subagent.PermissionExplore}
			}
			worker, err := executor.RunBackground(req)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = worker.Stop(); worker.WaitForCompletion(time.Second) })
			// Change the actual parent state after the launch returns, while the
			// provider holds the worker before its tool call.
			current.Store(tc.later)
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not reach provider barrier")
			}
			close(release)
			if !worker.WaitForCompletion(5 * time.Second) {
				t.Fatal("worker did not finish")
			}
			info := worker.GetStatus()
			if info.Status != task.StatusCompleted || info.PermissionMode != string(tc.want) {
				t.Fatalf("launch snapshot changed: %+v", info)
			}
			_, err = os.Stat(path)
			if tc.wantWrite && err != nil {
				t.Fatalf("launch permission was revoked mid-run: %v", err)
			}
			if !tc.wantWrite && !os.IsNotExist(err) {
				t.Fatalf("parent mode change elevated a worker: %v", err)
			}
		})
	}
}
