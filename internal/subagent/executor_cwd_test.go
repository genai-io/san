package subagent

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/core/system"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

type cwdHandoffTool struct {
	tool.Tool
	cwd string
}

func (t cwdHandoffTool) Name() string { return "CwdHandoff" }
func (t cwdHandoffTool) Execute(context.Context, map[string]any, string) toolresult.ToolResult {
	return toolresult.ToolResult{Success: true, HookResponse: map[string]any{"cwd": t.cwd}}
}

func putCwdHandoff(t *testing.T, id, cwd string) {
	t.Helper()
	adapter := tool.AdaptTool(cwdHandoffTool{cwd: cwd}, core.ToolSchema{Name: "CwdHandoff"}, nil)
	if _, err := adapter.Run(core.WithToolCallID(context.Background(), id), ai.ToolCall{ID: id, Input: `{}`}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerCwdBatchOrderAndCancellation(t *testing.T) {
	for _, reversed := range []bool{false, true} {
		for _, canceled := range []bool{false, true} {
			initial := t.TempDir()
			first, last := filepath.Join(initial, "first"), filepath.Join(initial, "last")
			run := &preparedRun{cwd: initial, system: system.Build(core.ScopeSubagent, system.WithEnvironment(system.Environment{Cwd: initial}))}
			ctx, cancel := context.WithCancel(context.Background())
			run.forwardEvent(ctx, sdkagent.ToolStart{ID: "first"})
			run.forwardEvent(ctx, sdkagent.ToolStart{ID: "last"})
			putCwdHandoff(t, "first", first)
			putCwdHandoff(t, "last", last)
			ids := []string{"first", "last"}
			if reversed {
				ids[0], ids[1] = ids[1], ids[0]
			}
			if canceled {
				cancel()
			}
			var readers sync.WaitGroup
			stop := make(chan struct{})
			for range 4 {
				readers.Go(func() {
					for {
						select {
						case <-stop:
							return
						default:
							run.currentCwd()
						}
					}
				})
			}
			run.forwardEvent(ctx, sdkagent.ToolEnd{ID: ids[0]})
			if run.currentCwd() != initial {
				t.Fatal("a sibling saw a mid-batch cwd mutation")
			}
			run.forwardEvent(ctx, sdkagent.ToolEnd{ID: ids[1]})
			close(stop)
			readers.Wait()
			cancel()
			want := last
			if canceled {
				want = initial
			}
			if got := run.currentCwd(); got != want {
				t.Fatalf("reversed=%v canceled=%v cwd=%s want=%s", reversed, canceled, got, want)
			}
			for _, id := range ids {
				if effect := tool.PopSideEffect(id); effect != nil {
					t.Fatalf("handoff leaked: %+v", effect)
				}
			}
		}
	}
}
