//go:build !windows

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/genai-io/san/internal/llm"
	"github.com/genai-io/san/internal/setting"
	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/sdk-go/pkg/ai"
	"github.com/genai-io/sdk-go/pkg/ai/aitest"
)

// Exercise the real application boundary and Bash adapter, not RunOnce: a
// completed agent turn alone must not own application service shutdown.
func TestPrintStopsServicesOnExit(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "agent_error"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("SAN_TRUST_WORKSPACE", "1")
			t.Chdir(t.TempDir())
			if err := os.Mkdir(".san", 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(".san", "settings.local.json"), []byte(`{"autoMemory":false,"permissions":{"allow":["Bash(*)"]}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := task.Initialize(task.Options{OutputDir: t.TempDir()}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = task.Default().StopServices(""); task.ResetDefaultTracker() })

			boom := errors.New("print lifecycle agent failure")
			p := &printLifecycleProvider{t: t, fail: fail, boom: boom}
			p.client = ai.NewClientWithDriver(aitest.New(
				aitest.Asks(ai.ToolCall{ID: "service", Name: "Bash", Input: `{"command":"exec sleep 60","run_in_background":true,"lifetime":"service"}`}),
				aitest.Says("done"),
			), ai.Model{ID: "print-lifecycle", API: "aitest", ContextWindow: 200000})
			id := llm.ProviderID("print-lifecycle-test")
			llm.Register(llm.Meta{Provider: id, AuthMethod: llm.AuthAPIKey}, func(context.Context) (llm.Provider, error) { return p, nil })
			t.Cleanup(func() { llm.Unregister(id, llm.AuthAPIKey) })
			store, err := llm.NewStore()
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SetCurrentModel("print-lifecycle", id, llm.AuthAPIKey); err != nil {
				t.Fatal(err)
			}
			err = Run(setting.RunOptions{Print: "launch a service"})
			if fail && (err == nil || !errors.Is(err, boom)) {
				t.Fatalf("wanted agent error, got %v", err)
			}
			if !fail && err != nil {
				t.Fatal(err)
			}
			if p.service == nil {
				t.Fatal("agent never launched a service")
			}
			if info := p.service.GetStatus(); info.Status != task.StatusStopped || p.service.IsRunning() {
				t.Fatalf("service survived print exit: %+v", info)
			}
			deadline := time.Now().Add(3 * time.Second)
			for syscall.Kill(p.pid, 0) == nil && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if err := syscall.Kill(p.pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("service process %d survived: %v", p.pid, err)
			}
		})
	}
}

type printLifecycleProvider struct {
	t       *testing.T
	client  *ai.Client
	fail    bool
	boom    error
	calls   int
	service task.BackgroundTask
	pid     int
}

func (p *printLifecycleProvider) Name() string { return "print-lifecycle-test:api_key" }
func (p *printLifecycleProvider) ListModels(context.Context) ([]llm.ModelInfo, error) {
	return nil, nil
}
func (p *printLifecycleProvider) Client(string, map[string]string) (*ai.Client, error) {
	p.calls++
	if p.calls > 1 {
		services := task.Default().List()
		if len(services) != 1 {
			p.t.Fatalf("wanted one launched service, got %d", len(services))
		}
		p.service = services[0]
		info := p.service.GetStatus()
		p.pid = info.PID
		if info.Lifetime != task.LifetimeService || !p.service.IsRunning() || p.pid <= 0 {
			p.t.Fatalf("service not running before print exit: %+v", info)
		}
		if p.fail {
			return nil, p.boom
		}
	}
	return p.client, nil
}
