package app

import (
	"context"
	"fmt"
	"iter"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func TestACPBackgroundOutputAndOwnership(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell fixture")
	}
	var id string
	control := func(name string) aitest.Turn {
		return func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
			return aitest.Asks(ai.ToolCall{ID: "control", Name: name, Input: fmt.Sprintf(`{"task_id":%q}`, id)})(ctx, req)
		}
	}
	driver := aitest.New(
		aitest.Asks(ai.ToolCall{ID: "launch", Name: "Bash", Input: `{"command":"printf acp-task-result","execution_mode":"background_job"}`}), aitest.Says("launched"),
		control("TaskOutput"), aitest.Says("read"),
		control("TaskOutput"), aitest.Says("isolated"),
	)
	f := newACPFixture(t, driver, func(context.Context, protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
		return acpChoice("allow_once"), nil
	})
	a, b := f.session(t, t.TempDir()), f.session(t, t.TempDir())
	if _, err := f.prompt(a, "launch"); err != nil {
		t.Fatal(err)
	}
	f.host.mu.Lock()
	pool, other := f.host.sessions[a].runtimeBuildParams.Tasks, f.host.sessions[b].runtimeBuildParams.Tasks
	f.host.mu.Unlock()
	pool.Join()
	jobs := pool.List()
	if len(jobs) != 1 {
		t.Fatalf("tasks = %d", len(jobs))
	}
	id = jobs[0].GetID()
	if info := jobs[0].GetStatus(); info.Status != task.StatusCompleted || info.Output != "acp-task-result" || info.ParentSessionID != string(a) {
		t.Fatalf("task = %+v", info)
	}
	if _, exists := other.Get(id); exists {
		t.Fatal("task leaked to another session")
	}
	if _, exists := task.Default().Get(id); exists {
		t.Fatal("task leaked to native default")
	}
	if _, err := f.prompt(a, "read output"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.prompt(b, "read foreign output"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var own, foreign bool
	for _, update := range f.updates {
		u, ok := update.Update.Value.(protocol.SessionUpdateToolCallUpdateVariant)
		if !ok || u.ToolCallID != "control" {
			continue
		}
		if update.SessionID == a && u.Status.Value == protocol.ToolCallStatusCompleted {
			own = true
		}
		if update.SessionID == b && u.Status.Value == protocol.ToolCallStatusFailed {
			foreign = true
		}
	}
	if !own || !foreign {
		t.Fatalf("owner success=%v foreign rejection=%v", own, foreign)
	}
}

func TestACPBackgroundDenied(t *testing.T) {
	for _, choice := range []string{"reject_once", "unoffered"} {
		t.Run(choice, func(t *testing.T) {
			f := newACPFixture(t, aitest.New(aitest.Asks(ai.ToolCall{ID: "launch", Name: "Bash", Input: `{"command":"sleep 60","execution_mode":"background_service"}`}), aitest.Says("denied")), func(context.Context, protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
				return acpChoice(choice), nil
			})
			id := f.session(t, t.TempDir())
			if _, err := f.prompt(id, "launch"); err != nil {
				t.Fatal(err)
			}
			f.host.mu.Lock()
			pool := f.host.sessions[id].runtimeBuildParams.Tasks
			f.host.mu.Unlock()
			if len(pool.List()) != 0 {
				t.Fatal("denied launch created a task")
			}
		})
	}
}

func TestACPBackgroundConcurrentSessions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell fixture")
	}
	f := newACPFixture(t, aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
		for _, message := range req.Messages {
			for _, block := range message.Content {
				if block.Type == ai.BlockToolResult {
					return aitest.Says("launched")(ctx, req)
				}
			}
		}
		return aitest.Asks(ai.ToolCall{ID: "same-call-id", Name: "Bash", Input: `{"command":"printf isolated","execution_mode":"background_job"}`})(ctx, req)
	}), func(context.Context, protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
		return acpChoice("allow_once"), nil
	})
	ids := []protocol.SessionID{f.session(t, t.TempDir()), f.session(t, t.TempDir())}
	done := make(chan error, len(ids))
	for _, id := range ids {
		go func() { _, err := f.prompt(id, "launch"); done <- err }()
	}
	for range ids {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	var tasks []task.BackgroundTask
	for _, id := range ids {
		f.host.mu.Lock()
		pool := f.host.sessions[id].runtimeBuildParams.Tasks
		f.host.mu.Unlock()
		pool.Join()
		jobs := pool.List()
		if len(jobs) != 1 || jobs[0].GetStatus().ParentSessionID != string(id) {
			t.Fatal("task ownership lost")
		}
		tasks = append(tasks, jobs[0])
	}
	if tasks[0].GetID() == tasks[1].GetID() {
		t.Fatal("shared task identity")
	}
}

func TestACPBackgroundCancelAndDisconnect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell fixture")
	}
	for _, explicit := range []bool{true, false} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			var id string
			var approvals, stops atomic.Int32
			driver := aitest.New(
				aitest.Asks(ai.ToolCall{ID: "launch", Name: "Bash", Input: `{"command":"sleep 60","execution_mode":"background_service"}`}), aitest.Says("launched"),
				func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
					return aitest.Asks(ai.ToolCall{ID: "stop", Name: "TaskStop", Input: fmt.Sprintf(`{"task_id":%q}`, id)})(ctx, req)
				}, aitest.Says("stopped"),
			)
			f := newACPFixture(t, driver, func(_ context.Context, request protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
				if request.ToolCall.ToolCallID == "launch" {
					approvals.Add(1)
				}
				if request.ToolCall.ToolCallID == "stop" {
					stops.Add(1)
				}
				return acpChoice("allow_once"), nil
			})
			sessionID := f.session(t, t.TempDir())
			if _, err := f.prompt(sessionID, "start service"); err != nil {
				t.Fatal(err)
			}
			f.host.mu.Lock()
			pool := f.host.sessions[sessionID].runtimeBuildParams.Tasks
			f.host.mu.Unlock()
			jobs := pool.List()
			if len(jobs) != 1 || !jobs[0].IsRunning() {
				t.Fatalf("service not running: %+v", jobs)
			}
			id = jobs[0].GetID()
			if approvals.Load() != 1 {
				t.Fatalf("launch permission requests = %d", approvals.Load())
			}
			if explicit {
				if _, err := f.prompt(sessionID, "stop service"); err != nil {
					t.Fatal(err)
				}
				if stops.Load() != 1 {
					t.Fatalf("stop permission requests = %d", stops.Load())
				}
			} else {
				_ = f.client.Close()
				select {
				case err := <-f.stopped:
					if err != nil && !strings.Contains(err.Error(), "closed") {
						t.Fatal(err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("disconnect did not join tasks")
				}
			}
			joined := make(chan struct{})
			go func() { pool.Join(); close(joined) }()
			select {
			case <-joined:
			case <-time.After(3 * time.Second):
				t.Fatal("task execution not joined")
			}
			if jobs[0].IsRunning() {
				t.Fatal("task survived cancellation")
			}
		})
	}
}
