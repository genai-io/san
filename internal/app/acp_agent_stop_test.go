package app

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestACPAgentStopOwnership(t *testing.T) {
	var taskID string
	started := make(chan struct{}, 1)
	driver := aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		if workerRequest(req) {
			return func(yield func(ai.Delta, error) bool) {
				select {
				case started <- struct{}{}:
				default:
				}
				<-ctx.Done()
				yield(ai.Delta{}, ctx.Err())
			}
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role == ai.RoleUser && strings.Contains(last.Content.Text(), "stop worker") {
			return aitest.Asks(ai.ToolCall{ID: "stop", Name: "TaskStop", Input: fmt.Sprintf(`{"task_id":%q}`, taskID)})(ctx, req)
		}
		if hasResult(req) {
			return aitest.Says("done")(ctx, req)
		}
		return aitest.Asks(ai.ToolCall{ID: "launch", Name: "Agent", Input: `{"prompt":"WORKER_BRIEF","run_in_background":true}`})(ctx, req)
	})
	f := newACPFixture(t, driver, func(context.Context, protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
		return acpChoice("allow_once"), nil
	})
	own, foreign := f.session(t, t.TempDir()), f.session(t, t.TempDir())
	if _, err := f.prompt(own, "delegate"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("worker not started")
	}
	f.host.mu.Lock()
	pool := f.host.sessions[own].runtimeBuildParams.Tasks
	f.host.mu.Unlock()
	jobs := pool.List()
	if len(jobs) != 1 {
		t.Fatalf("jobs=%d", len(jobs))
	}
	taskID = jobs[0].GetID()
	if _, err := f.prompt(foreign, "stop worker"); err != nil {
		t.Fatal(err)
	}
	if !jobs[0].IsRunning() {
		t.Fatal("foreign session stopped worker")
	}
	if _, err := f.prompt(own, "stop worker"); err != nil {
		t.Fatal(err)
	}
	joined := make(chan struct{})
	go func() { pool.Join(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not join worker")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	rejected := false
	for _, n := range f.updates {
		if u, ok := n.Update.Value.(protocol.SessionUpdateToolCallUpdateVariant); ok && n.SessionID == foreign && u.ToolCallID == "stop" && u.Status.Value == protocol.ToolCallStatusFailed {
			rejected = true
		}
	}
	if !rejected {
		t.Fatal("foreign stop was not rejected by protocol tool result")
	}
}
