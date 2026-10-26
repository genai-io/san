package fs

import (
	"context"
	"testing"
	"time"

	"github.com/genai-io/san/internal/task"
)

func TestShellExecutionValidation(t *testing.T) {
	shell := &ShellTool{}
	for _, params := range []map[string]any{
		{"execution_mode": "invalid"},
		{"execution_mode": true},
		{"execution_mode": shellBackgroundService, "timeout_ms": 1},
		{"execution_mode": shellBackgroundJob, "run_in_background": false},
		{"execution_mode": shellForeground, "lifetime": "service"},
		{"timeout_ms": 1, "timeout": 2},
		{"timeout_ms": 0},
		{"timeout_ms": -1},
		{"timeout_ms": 1.5},
		{"timeout_ms": maxShellTimeoutMS + 1},
		{"timeout_ms": "1000"},
		{"lifetime": "invalid", "run_in_background": true},
		{"lifetime": "service"},
		{"lifetime": "service", "run_in_background": true, "timeout": 1},
		{"lifetime": 42, "run_in_background": true},
		{"run_in_background": "false"},
	} {
		params["command"] = "exit 0"
		if _, err := shell.PreparePermission(context.Background(), params, t.TempDir()); err == nil {
			t.Fatalf("permission accepted %v", params)
		}
		if result := shell.ExecuteApproved(context.Background(), params, t.TempDir()); result.Success {
			t.Fatalf("execution accepted %v", params)
		}
	}
	options, err := parseShellExecutionOptions(map[string]any{})
	if err != nil || options.background || options.lifetime != task.LifetimeJob || options.timeout != 120*time.Second {
		t.Fatalf("default: %+v %v", options, err)
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
