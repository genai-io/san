package hook

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/genai-io/san/internal/setting"
)

func TestShutdownCancelsAsyncCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	for _, rewake := range []bool{false, true} {
		t.Run(map[bool]string{false: "async", true: "asyncRewake"}[rewake], func(t *testing.T) {
			dir := t.TempDir()
			settings := setting.NewData()
			settings.Hooks["SessionEnd"] = []setting.Hook{{Hooks: []setting.HookCmd{{Type: "command", Command: "touch started; exec sleep 30", Async: !rewake, AsyncRewake: rewake}}}}
			engine := NewEngine(settings, "owner", dir, "")
			audited := make(chan HookFiredAudit, 2)
			engine.SetAuditCallback(func(a HookFiredAudit) { audited <- a })
			engine.Execute(context.Background(), SessionEnd, HookInput{})
			deadline := time.Now().Add(3 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					engine.Shutdown()
					t.Fatal("command did not start")
				}
				time.Sleep(time.Millisecond)
			}
			start := time.Now()
			engine.Shutdown()
			// Allow the command executor's five-second pipe-drain backstop.
			if time.Since(start) > 10*time.Second {
				t.Fatal("shutdown did not cancel delayed command")
			}
			if len(audited) != 2 {
				t.Fatalf("missing launch/completion audit: %d", len(audited))
			}
			first, last := <-audited, <-audited
			if first.Outcome != outcomeAsync || last.Outcome != outcomeError {
				t.Fatalf("audits: %+v %+v", first, last)
			}
		})
	}
}

func TestShutdownJoinsDetachedAudit(t *testing.T) {
	engine := NewEngine(&setting.Data{}, "owner", t.TempDir(), "")
	started := make(chan struct{})
	audited := make(chan HookFiredAudit, 1)
	engine.SetAuditCallback(func(a HookFiredAudit) { audited <- a })
	engine.AddSessionFunctionHook(FileChanged, "", FunctionHook{Callback: func(ctx context.Context, _ HookInput) (HookOutput, error) {
		close(started)
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond) // cleanup must finish before the writer closes
		return HookOutput{}, ctx.Err()
	}})
	engine.ExecuteAsync(FileChanged, HookInput{})
	<-started
	engine.Shutdown()
	select {
	case a := <-audited:
		if a.Outcome != outcomeError {
			t.Fatalf("audit: %+v", a)
		}
	default:
		t.Fatal("shutdown returned before completion audit")
	}
	engine.ExecuteAsync(FileChanged, HookInput{}) // admission is closed
	engine.Shutdown()
}
