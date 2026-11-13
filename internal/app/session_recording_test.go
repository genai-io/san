package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/genai-io/san/internal/hook"
	"github.com/genai-io/san/internal/session"
)

func TestSessionShutdownDrainsHookBeforeClosingWriter(t *testing.T) {
	m, _ := resumedModel(t, nil)
	owner := m.services.Session
	engine := m.services.Hook
	bindSessionHooks(engine, nil, owner, "")
	started := make(chan struct{})
	engine.AddRuntimeFunctionHook(hook.PostToolUse, "", hook.FunctionHook{Callback: func(ctx context.Context, _ hook.HookInput) (hook.HookOutput, error) {
		close(started)
		<-ctx.Done()
		return hook.HookOutput{}, nil
	}})
	engine.ExecuteAsync(hook.PostToolUse, hook.HookInput{ToolName: "Bash"})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("detached hook never started")
	}
	if err := m.closeSessionRecording(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(owner.Err(), session.ErrClosed) {
		t.Fatal("session writer remains open after hook shutdown")
	}
	projection := owner.Snapshot()
	finishes := 0
	for _, event := range projection.Events() {
		if payload, ok := event.Payload.(*session.HookFinishPayload); ok {
			finishes++
			if payload.Trigger != string(hook.PostToolUse) {
				t.Fatalf("unexpected hook outcome: %+v", payload)
			}
		}
	}
	if finishes != 1 {
		t.Fatalf("detached outcome was lost before writer close: %d hook finishes", finishes)
	}
}
