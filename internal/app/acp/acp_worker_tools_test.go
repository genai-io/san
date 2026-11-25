package acp

import (
	"context"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestACPWorkerReceivesOnlySelectedTools(t *testing.T) {
	for _, tc := range []struct {
		tools      string
		want       []string
		background bool
	}{{`["Read"]`, []string{"Read"}, false}, {`[]`, nil, true}} {
		t.Run(tc.tools, func(t *testing.T) {
			observed := make(chan []string, 1)
			driver := aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
				if hasResult(req) {
					return aitest.Says("done")(ctx, req)
				}
				if workerRequest(req) {
					var names []string
					for _, schema := range req.Tools {
						names = append(names, schema.Name)
					}
					observed <- names
					// An unadvertised call must not be executable, even with a permissive owner.
					return aitest.Asks(ai.ToolCall{ID: "unexpected-write", Name: "Write", Input: `{"file_path":"unexpected.txt","content":"no"}`})(ctx, req)
				}
				return aitest.Asks(ai.ToolCall{ID: "launch", Name: "Agent", Input: fmt.Sprintf(`{"prompt":"WORKER_BRIEF","description":"limit worker","run_in_background":%t,"tools":%s}`, tc.background, tc.tools)})(ctx, req)
			})
			f := newACPFixture(t, driver, func(context.Context, protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
				return acpChoice("allow_once"), nil
			})
			cwd := t.TempDir()
			id := f.session(t, cwd)
			if _, err := f.prompt(id, "delegate"); err != nil {
				t.Fatal(err)
			}
			f.host.mu.Lock()
			s := f.host.sessions[id]
			f.host.mu.Unlock()
			s.runtimeBuildParams.Tasks.Join()
			select {
			case got := <-observed:
				if !slices.Equal(got, tc.want) {
					t.Fatalf("worker catalogue = %v; want %v", got, tc.want)
				}
			default:
				t.Fatal("worker did not run")
			}
			if _, err := os.Stat(filepath.Join(cwd, "unexpected.txt")); !os.IsNotExist(err) {
				t.Fatalf("unexpected write: %v", err)
			}
		})
	}
}
