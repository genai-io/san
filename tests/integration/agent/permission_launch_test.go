package subagent_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/subagent"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/tests/integration/testutil"
)

func TestBackgroundPermissionsAreResolvedBeforeLaunch(t *testing.T) {
	old := subagent.Default()
	subagent.SetDefaultRegistry(subagent.NewRegistry())
	t.Cleanup(func() { subagent.SetDefaultRegistry(old) })
	for _, tc := range []struct {
		name, agent, mode string
		wantMode          subagent.PermissionMode
		wantWrite         bool
	}{
		{"default general inherits YOLO", "", "default", subagent.PermissionBypass, true},
		{"general label inherits YOLO", "general", "default", subagent.PermissionBypass, true},
		{"explicit explorer stays read-only", "", "explore", subagent.PermissionExplore, false},
		{"explicit edit stays narrower", "", "edit", subagent.PermissionAcceptEdits, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := task.Initialize(task.Options{OutputDir: t.TempDir()}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(task.ResetDefaultTracker)
			cwd := t.TempDir()
			path := filepath.Join(cwd, "result.txt")
			input, err := json.Marshal(map[string]any{"file_path": path, "content": "written"})
			if err != nil {
				t.Fatal(err)
			}
			provider := &testutil.FakeProvider{Responses: []llm.CompletionResponse{
				testutil.ToolCallResponse("Write", "write-result", string(input)),
				testutil.EndTurnResponse("finished"),
			}}
			executor := subagent.NewExecutor(provider, cwd, "fake-model", nil)
			var parentReads atomic.Int32
			executor.SetParentPermissionMode(func() subagent.PermissionMode {
				if parentReads.Add(1) == 1 {
					return subagent.PermissionBypass
				}
				// Simulate the parent changing modes after the launch snapshot.
				return subagent.PermissionExplore
			})
			worker, err := executor.RunBackground(tool.AgentExecRequest{Agent: tc.agent, Mode: tc.mode, Prompt: "Write a result"})
			if err != nil {
				t.Fatal(err)
			}
			if got := worker.GetStatus().PermissionMode; got != string(tc.wantMode) {
				t.Fatalf("launch mode = %q, want %q", got, tc.wantMode)
			}
			if !worker.WaitForCompletion(5 * time.Second) {
				_ = worker.Stop()
				t.Fatal("worker did not finish")
			}
			info := worker.GetStatus()
			if info.Status != task.StatusCompleted {
				t.Fatalf("run failed: %+v", info)
			}
			_, err = os.Stat(path)
			if tc.wantWrite && err != nil {
				t.Fatalf("inherited worker could not write: %v", err)
			}
			if !tc.wantWrite && !os.IsNotExist(err) {
				t.Fatalf("explorer escaped its read-only ceiling: %v", err)
			}
			wantReads := int32(0)
			if tc.mode == "default" {
				wantReads = 1
			}
			if got := parentReads.Load(); got != wantReads {
				t.Fatalf("parent mode read %d times, want %d", got, wantReads)
			}
			log, err := os.ReadFile(info.OutputFile)
			if err != nil || !strings.Contains(string(log), `"event":"agent.permissions"`) || !strings.Contains(string(log), `"mode":"`+string(tc.wantMode)+`"`) {
				t.Fatalf("launch policy missing from task log: %s, %v", log, err)
			}
		})
	}
}
