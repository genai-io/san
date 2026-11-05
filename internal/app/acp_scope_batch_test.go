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

func TestACPWorkerBatchDirectorySnapshot(t *testing.T) {
	cwd, outside := t.TempDir(), t.TempDir()
	var writes atomic.Int32
	driver := aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		if hasResult(req) {
			return aitest.Says("done")(ctx, req)
		}
		if workerRequest(req) {
			return aitest.Asks(
				ai.ToolCall{ID: "cd", Name: "Bash", Input: fmt.Sprintf(`{"command":%q}`, "cd '"+outside+"'")},
				ai.ToolCall{ID: "write", Name: "Write", Input: `{"file_path":"snapshot.txt","content":"owned"}`},
			)(ctx, req)
		}
		return aitest.Asks(ai.ToolCall{ID: "launch", Name: "Agent", Input: `{"prompt":"WORKER_BRIEF"}`})(ctx, req)
	})
	f := newACPFixture(t, driver, func(_ context.Context, r protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
		if r.ToolCall.ToolCallID == "write" {
			writes.Add(1)
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
	if _, err := os.Stat(filepath.Join(cwd, "snapshot.txt")); err != nil {
		t.Fatal("authorized snapshot was not used:", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "snapshot.txt")); !os.IsNotExist(err) {
		t.Fatalf("batch write escaped authorization: %v", err)
	}
	if writes.Load() != 0 {
		t.Fatal("inside write prompted")
	}
}
