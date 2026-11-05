package app

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestACPExecutionDirectoryPermissions(t *testing.T) {
	for _, worker := range []bool{false, true} {
		for _, outside := range []bool{false, true} {
			for _, background := range []bool{false, true} {
				if !worker && (outside || background) {
					continue
				}
				t.Run(fmt.Sprintf("worker=%t/outside=%t/background=%t", worker, outside, background), func(t *testing.T) {
					cwd, target := t.TempDir(), t.TempDir()
					if outside && !background {
						// Reproduce the bypass: a relative path used to be checked
						// against process cwd, which is inside the parent's scope.
						var err error
						cwd, err = os.Getwd()
						if err != nil {
							t.Fatal(err)
						}
					}
					if !outside {
						target = cwd
					}
					var step, writes atomic.Int32
					driver := aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
						if worker && !workerRequest(req) {
							if hasResult(req) {
								return aitest.Says("parent done")(ctx, req)
							}
							return aitest.Asks(ai.ToolCall{ID: "launch", Name: "Agent", Input: fmt.Sprintf(`{"prompt":"WORKER_BRIEF","run_in_background":%t}`, background)})(ctx, req)
						}
						switch step.Add(1) {
						case 1:
							if outside {
								return aitest.Asks(ai.ToolCall{ID: "cd", Name: "Bash", Input: fmt.Sprintf(`{"command":%q}`, "cd '"+target+"'")})(ctx, req)
							}
							fallthrough
						case 2:
							if !outside && step.Load() == 2 {
								return aitest.Says("done")(ctx, req)
							}
							return aitest.Asks(ai.ToolCall{ID: "write", Name: "Write", Input: `{"file_path":"scoped.txt","content":"owned"}`})(ctx, req)
						default:
							return aitest.Says("done")(ctx, req)
						}
					})
					if background {
						if err := os.MkdirAll(filepath.Join(cwd, ".san"), 0700); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(cwd, ".san", "settings.local.json"), []byte(`{"permissions":{"allow":["Bash(**)"]}}`), 0600); err != nil {
							t.Fatal(err)
						}
					}
					f := newACPFixture(t, driver, func(_ context.Context, r protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
						if r.ToolCall.ToolCallID == "write" {
							writes.Add(1)
							var input map[string]any
							if err := json.Unmarshal(r.ToolCall.RawInput.Value, &input); err != nil || input["file_path"] != filepath.Join(target, "scoped.txt") {
								t.Errorf("approval did not show execution path: %s (%v)", r.ToolCall.RawInput.Value, err)
							}
							return acpChoice("reject_once"), nil
						}
						return acpChoice("allow_once"), nil
					})
					id := f.session(t, cwd)
					var response protocol.SetSessionModeResponse
					if err := f.client.Call(f.ctx, "session/set_mode", protocol.SetSessionModeRequest{SessionID: id, ModeID: "auto-accept"}, &response); err != nil {
						t.Fatal(err)
					}
					if _, err := f.prompt(id, "run"); err != nil {
						t.Fatal(err)
					}
					f.host.mu.Lock()
					s := f.host.sessions[id]
					f.host.mu.Unlock()
					s.params.Tasks.Join()
					_, err := os.Stat(filepath.Join(target, "scoped.txt"))
					if (err == nil) != !outside {
						t.Fatalf("write exists=%v outside=%v", err == nil, outside)
					}
					want := int32(0)
					if outside && !background {
						want = 1
					}
					if writes.Load() != want {
						t.Fatalf("approvals=%d want=%d", writes.Load(), want)
					}
					if outside && step.Load() < 3 {
						t.Fatal("worker did not reach write after cd")
					}
				})
			}
		}
	}
}
