package subagent

import (
	"context"
	"testing"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/session"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

type childStartHooks struct{ dispatch func(hook.EventType) }

func (h childStartHooks) Execute(context.Context, hook.EventType, hook.HookInput) hook.HookOutcome {
	return hook.HookOutcome{}
}
func (h childStartHooks) ExecuteAsync(event hook.EventType, _ hook.HookInput) { h.dispatch(event) }
func (childStartHooks) HasHooks(hook.EventType) bool                          { return false }
func (childStartHooks) StopHookActive() *bool                                 { return nil }

func TestTypedCreationPrecedesLifecycleHooks(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "open", true: "closed"}[closed], func(t *testing.T) {
			ctx := context.Background()
			cwd := t.TempDir()
			store, err := session.OpenStore(ctx, cwd)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			parent, err := store.Create(ctx, session.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer parent.Close()
			var run *preparedRun
			starts, stops := 0, 0
			hooks := childStartHooks{dispatch: func(event hook.EventType) {
				switch event {
				case hook.SubagentStart:
					starts++
					if run.recording == nil {
						t.Error("start hook preceded child owner")
						return
					}
					overview, err := store.Get(ctx, run.recording.ID())
					if err != nil || overview.Parent != parent.ID() {
						t.Errorf("start hook lacks committed original child: %+v %v", overview, err)
					}
					snapshot := run.recording.Snapshot()
					link := snapshot.Identity().Parent
					if link == nil || link.CallID != "launch" {
						t.Errorf("start hook lacks original call: %+v", link)
					}
				case hook.SubagentStop:
					stops++
				}
			}}
			d := aitest.New(aitest.Says("done"))
			e := NewExecutor(continuationProvider{d}, cwd, "aitest", hooks)
			if err := e.SetTypedSessions(store, parent); err != nil {
				t.Fatal(err)
			}
			run, err = e.prepareRun(ctx, tool.AgentExecRequest{Prompt: "work", ParentToolCallID: "launch"})
			if err != nil {
				t.Fatal(err)
			}
			if closed {
				store.Close()
			}
			_, err = e.runPrepared(ctx, run)
			if closed {
				if err == nil || starts != 0 || stops != 0 || d.Calls() != 0 {
					t.Fatalf("closed storage dispatched: starts=%d stops=%d calls=%d err=%v", starts, stops, d.Calls(), err)
				}
			} else if err != nil || starts != 1 || stops != 1 {
				t.Fatalf("lifecycle starts=%d stops=%d err=%v", starts, stops, err)
			}
		})
	}
}
