package subagent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/genai-io/san/internal/tool"
	sdkagent "github.com/genai-io/sdk-go/pkg/agent"
)

func TestWorkerCwdSameCallIDIsIsolated(t *testing.T) {
	ctx := context.Background()
	first := &preparedRun{cwd: t.TempDir()}
	second := &preparedRun{cwd: t.TempDir()}
	firstCwd := filepath.Join(first.cwd, "first")
	secondCwd := filepath.Join(second.cwd, "second")
	const id = "shared-call"
	first.forwardEvent(ctx, sdkagent.ToolStart{ID: id})
	second.forwardEvent(ctx, sdkagent.ToolStart{ID: id})
	putCwdHandoff(t, id, firstCwd, first)
	putCwdHandoff(t, id, secondCwd, second)
	first.forwardEvent(ctx, sdkagent.ToolEnd{ID: id})
	second.forwardEvent(ctx, sdkagent.ToolEnd{ID: id})
	if first.currentCwd() != firstCwd || second.currentCwd() != secondCwd {
		t.Fatalf("cross-worker handoff: first=%q second=%q", first.currentCwd(), second.currentCwd())
	}
	if effect := tool.PopSideEffect(id); effect != nil {
		t.Fatalf("worker handoff leaked to parent: %+v", effect)
	}
}

func TestWorkerDoesNotConsumeParentCwdHandoff(t *testing.T) {
	ctx := context.Background()
	run := &preparedRun{cwd: t.TempDir()}
	initial := run.currentCwd()
	const id = "parent-shared-call"
	parentCwd := t.TempDir()
	run.forwardEvent(ctx, sdkagent.ToolStart{ID: id})
	putCwdHandoff(t, id, parentCwd)
	run.forwardEvent(ctx, sdkagent.ToolEnd{ID: id})
	effect, _ := tool.PopSideEffect(id).(map[string]any)
	if run.currentCwd() != initial || effect["cwd"] != parentCwd {
		t.Fatalf("worker consumed parent handoff: cwd=%q effect=%+v", run.currentCwd(), effect)
	}
}
