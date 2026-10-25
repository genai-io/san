package fs

import (
	"context"
	"testing"
	"time"

	"github.com/genai-io/san/internal/task"
)

func TestShellLifetimeValidation(t *testing.T) {
	shell := &ShellTool{}
	for _, params := range []map[string]any{
		{"lifetime": "invalid", "run_in_background": true},
		{"lifetime": "job"},
		{"lifetime": "service", "run_in_background": true, "timeout": 1},
		{"lifetime": 42, "run_in_background": true},
	} {
		params["command"] = "exit 0"
		if _, err := shell.PreparePermission(context.Background(), params, t.TempDir()); err == nil {
			t.Fatalf("permission accepted %v", params)
		}
		if result := shell.ExecuteApproved(context.Background(), params, t.TempDir()); result.Success {
			t.Fatalf("execution accepted %v", params)
		}
	}
	if lifetime, err := shellLifetime(map[string]any{}); err != nil || lifetime != task.LifetimeJob {
		t.Fatalf("default: %s %v", lifetime, err)
	}
}

func TestServiceOutlivesJobTimeout(t *testing.T) {
	shell := &ShellTool{}
	result := shell.executeBackground(context.Background(), "sleep 5", "service", t.TempDir(), 20*time.Millisecond, task.LifetimeService)
	if !result.Success {
		t.Fatal(result.Error)
	}
	meta := result.HookResponse.(map[string]any)["backgroundTask"].(map[string]any)
	id := meta["taskId"].(string)
	service, ok := task.Default().Get(id)
	if !ok {
		t.Fatal("missing service")
	}
	defer task.Default().Remove(id)
	defer task.Default().Kill(id)
	time.Sleep(80 * time.Millisecond)
	if !service.IsRunning() {
		t.Fatalf("service inherited job deadline: %+v", service.GetStatus())
	}
	if err := task.Default().Kill(id); err != nil {
		t.Fatal(err)
	}
	if info := service.GetStatus(); info.Status != task.StatusStopped {
		t.Fatalf("cleanup failed: %+v", info)
	}
}
