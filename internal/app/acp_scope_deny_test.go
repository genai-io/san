package app

import (
	"context"
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

func TestACPScopedDenyRules(t *testing.T) {
	for _, worker := range []bool{false, true} {
		for _, absolute := range []bool{false, true} {
			t.Run(fmt.Sprintf("worker=%t/absolute=%t", worker, absolute), func(t *testing.T) {
				cwd := t.TempDir()
				path := "denied.txt"
				if absolute {
					path = filepath.Join(cwd, path)
				}
				if err := os.MkdirAll(filepath.Join(cwd, ".san"), 0700); err != nil {
					t.Fatal(err)
				}
				settings := fmt.Sprintf(`{"permissions":{"deny":[%q]}}`, "Write("+path+")")
				if err := os.WriteFile(filepath.Join(cwd, ".san", "settings.local.json"), []byte(settings), 0600); err != nil {
					t.Fatal(err)
				}
				var approvals atomic.Int32
				driver := aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
					if hasResult(req) {
						return aitest.Says("done")(ctx, req)
					}
					if worker && !workerRequest(req) {
						return aitest.Asks(ai.ToolCall{ID: "launch", Name: "Agent", Input: `{"prompt":"WORKER_BRIEF"}`})(ctx, req)
					}
					return aitest.Asks(ai.ToolCall{ID: "write", Name: "Write", Input: `{"file_path":"denied.txt","content":"denied"}`})(ctx, req)
				})
				f := newACPFixture(t, driver, func(_ context.Context, r protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
					if r.ToolCall.ToolCallID == "write" {
						approvals.Add(1)
					}
					return acpChoice("allow_once"), nil
				})
				id := f.session(t, cwd)
				var response protocol.SetSessionModeResponse
				if err := f.client.Call(f.ctx, "session/set_mode", protocol.SetSessionModeRequest{SessionID: id, ModeID: "auto-accept"}, &response); err != nil {
					t.Fatal(err)
				}
				if _, err := f.prompt(id, "write"); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Join(cwd, "denied.txt")); !os.IsNotExist(err) {
					t.Fatalf("deny did not hold: %v", err)
				}
				if approvals.Load() != 0 {
					t.Fatal("deny was offered to client for approval")
				}
			})
		}
	}
}
