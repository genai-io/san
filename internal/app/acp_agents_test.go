package app

import (
	"context"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/sdk-go/pkg/acp/protocol"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

func workerRequest(req *ai.Request) bool {
	for _, msg := range req.Messages {
		if strings.Contains(msg.Content.Text(), "WORKER_BRIEF") {
			return true
		}
	}
	return false
}
func hasResult(req *ai.Request) bool {
	for _, msg := range req.Messages {
		for _, b := range msg.Content {
			if b.Type == ai.BlockToolResult {
				return true
			}
		}
	}
	return false
}

func TestACPAgentExecution(t *testing.T) {
	for _, background := range []bool{false, true} {
		t.Run(fmt.Sprint(background), func(t *testing.T) {
			var approvals atomic.Int32
			driver := aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
				if workerRequest(req) {
					return aitest.Says("worker completed")(ctx, req)
				}
				if hasResult(req) {
					return aitest.Says("parent completed")(ctx, req)
				}
				return aitest.Asks(ai.ToolCall{ID: "launch", Name: "Agent", Input: fmt.Sprintf(`{"prompt":"WORKER_BRIEF","description":"worker test","run_in_background":%t}`, background)})(ctx, req)
			})
			f := newACPFixture(t, driver, func(context.Context, protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
				approvals.Add(1)
				return acpChoice("allow_once"), nil
			})
			id := f.session(t, t.TempDir())
			if _, err := f.prompt(id, "delegate"); err != nil {
				t.Fatal(err)
			}
			f.host.mu.Lock()
			s := f.host.sessions[id]
			f.host.mu.Unlock()
			s.params.Tasks.Join()
			jobs := s.params.Tasks.List()
			if len(jobs) != 1 {
				t.Fatalf("jobs=%d approvals=%d updates=%+v", len(jobs), approvals.Load(), f.updates)
			}
			info := jobs[0].GetStatus()
			if info.Status != task.StatusCompleted || !strings.Contains(info.Output, "worker completed") || info.ParentSessionID != string(id) {
				t.Fatalf("worker=%+v", info)
			}
			if info.AgentSessionID == "" {
				t.Fatal("missing native child session")
			}
			if _, ok := task.Default().Get(info.ID); ok {
				t.Fatal("worker escaped session pool")
			}
		})
	}
}

func TestACPAgentPermissionOwnership(t *testing.T) {
	for _, background := range []bool{false, true} {
		for _, deny := range []bool{false, true} {
			t.Run(fmt.Sprintf("background=%t/deny=%t", background, deny), func(t *testing.T) {
				var writes atomic.Int32
				driver := aitest.Always(func(ctx context.Context, req *ai.Request) iter.Seq2[ai.Delta, error] {
					if hasResult(req) {
						return aitest.Says("done")(ctx, req)
					}
					if workerRequest(req) {
						return aitest.Asks(ai.ToolCall{ID: "write", Name: "Write", Input: `{"file_path":"result.txt","content":"worker write"}`})(ctx, req)
					}
					return aitest.Asks(ai.ToolCall{ID: "launch", Name: "Agent", Input: fmt.Sprintf(`{"prompt":"WORKER_BRIEF","run_in_background":%t}`, background)})(ctx, req)
				})
				f := newACPFixture(t, driver, func(_ context.Context, r protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
					if r.ToolCall.ToolCallID == "write" {
						writes.Add(1)
					}
					return acpChoice("allow_once"), nil
				})
				cwd := t.TempDir()
				if deny {
					if err := os.MkdirAll(filepath.Join(cwd, ".san"), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(cwd, ".san", "settings.local.json"), []byte(`{"permissions":{"deny":["Write(**)"]}}`), 0600); err != nil {
						t.Fatal(err)
					}
				}
				id := f.session(t, cwd)
				if _, err := f.prompt(id, "delegate"); err != nil {
					t.Fatal(err)
				}
				f.host.mu.Lock()
				s := f.host.sessions[id]
				f.host.mu.Unlock()
				s.params.Tasks.Join()
				_, err := os.Stat(filepath.Join(cwd, "result.txt"))
				want := !background && !deny
				if (err == nil) != want {
					t.Fatalf("write exists=%v want=%v approvals=%d", err == nil, want, writes.Load())
				}
				if want && writes.Load() != 1 {
					t.Fatal("foreground worker did not use active permission receiver")
				}
				if !want && writes.Load() != 0 {
					t.Fatal("headless or denied worker requested human approval")
				}
			})
		}
	}
}

func TestACPAgentDisconnectReaps(t *testing.T) {
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
		if hasResult(req) {
			return aitest.Says("launched")(ctx, req)
		}
		return aitest.Asks(ai.ToolCall{ID: "launch", Name: "Agent", Input: `{"prompt":"WORKER_BRIEF","run_in_background":true}`})(ctx, req)
	})
	f := newACPFixture(t, driver, func(context.Context, protocol.RequestPermissionRequest) (protocol.RequestPermissionResponse, error) {
		return acpChoice("allow_once"), nil
	})
	id := f.session(t, t.TempDir())
	if _, err := f.prompt(id, "delegate"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("worker never started")
	}
	f.host.mu.Lock()
	pool := f.host.sessions[id].params.Tasks
	f.host.mu.Unlock()
	_ = f.client.Close()
	select {
	case <-f.stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect did not reap worker")
	}
	for _, job := range pool.List() {
		if job.IsRunning() {
			t.Fatal("worker survived disconnect")
		}
	}
}
