package fs

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool"
	"github.com/genai-io/san/internal/tool/toolresult"
	"github.com/genai-io/sdk-go/pkg/agent"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestShellExecutionOptions(t *testing.T) {
	for _, tc := range []struct {
		name       string
		params     map[string]any
		background bool
		lifetime   task.Lifetime
		timeout    time.Duration
	}{
		{"default", nil, false, task.LifetimeJob, 120 * time.Second},
		{"explicit foreground", map[string]any{"execution_mode": shellForeground}, false, task.LifetimeJob, 120 * time.Second},
		{"null defaults", map[string]any{"execution_mode": nil, "timeout_ms": nil, "description": nil}, false, task.LifetimeJob, 120 * time.Second},
		{"background job", map[string]any{"execution_mode": shellBackgroundJob, "timeout_ms": 1500}, true, task.LifetimeJob, 1500 * time.Millisecond},
		{"service", map[string]any{"execution_mode": shellBackgroundService, "timeout_ms": nil}, true, task.LifetimeService, 0},
		{"legacy foreground", map[string]any{"run_in_background": false, "lifetime": "job"}, false, task.LifetimeJob, 120 * time.Second},
		{"legacy default job", map[string]any{"lifetime": "job"}, false, task.LifetimeJob, 120 * time.Second},
		{"legacy background", map[string]any{"run_in_background": true, "timeout": 2000}, true, task.LifetimeJob, 2 * time.Second},
		{"legacy service", map[string]any{"run_in_background": true, "lifetime": "service", "timeout": nil}, true, task.LifetimeService, 0},
		{"legacy timeout cap", map[string]any{"timeout": float64(1e100)}, false, task.LifetimeJob, 600 * time.Second},
		{"matching modes", map[string]any{"execution_mode": shellBackgroundJob, "run_in_background": true, "lifetime": "job"}, true, task.LifetimeJob, 120 * time.Second},
		{"matching service", map[string]any{"execution_mode": shellBackgroundService, "lifetime": "service"}, true, task.LifetimeService, 0},
		{"matching timeouts", map[string]any{"timeout_ms": float64(3000), "timeout": 3000}, false, task.LifetimeJob, 3 * time.Second},
		{"integer duration", map[string]any{"timeout_ms": int64(4000)}, false, task.LifetimeJob, 4 * time.Second},
		{"JSON number", map[string]any{"timeout_ms": json.Number("5000")}, false, task.LifetimeJob, 5 * time.Second},
		{"null legacy fields", map[string]any{"execution_mode": shellForeground, "run_in_background": nil, "lifetime": nil, "timeout": nil}, false, task.LifetimeJob, 120 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := make(map[string]any, len(tc.params))
			for key, value := range tc.params {
				before[key] = value
			}
			got, err := parseShellExecutionOptions(tc.params)
			if err != nil || got.background != tc.background || got.lifetime != tc.lifetime || got.timeout != tc.timeout {
				t.Fatalf("options=%+v err=%v; want background=%t lifetime=%s timeout=%s", got, err, tc.background, tc.lifetime, tc.timeout)
			}
			if tc.params != nil && !reflect.DeepEqual(tc.params, before) {
				t.Fatal("argument normalization changed the original tool input")
			}
		})
	}
	for _, value := range []any{math.NaN(), math.Inf(1), math.Inf(-1), json.Number("invalid")} {
		if _, err := parseShellExecutionOptions(map[string]any{"timeout_ms": value}); err == nil {
			t.Fatalf("invalid numeric deadline accepted: %v", value)
		}
	}
}

func TestAdaptedShellDefaultsToForegroundWithoutTasks(t *testing.T) {
	task.Initialize(task.Options{})
	t.Cleanup(task.ResetDefaultTracker)
	worker := bashTool(t)
	adapted := tool.AdaptTool(worker, worker.Schema(), func() string { return t.TempDir() }, tool.WithSideEffectHandler(func(string, any) {}))
	for _, input := range []string{
		`{"command":"printf direct-result"}`,
		`{"command":"printf direct-result","description":null,"execution_mode":null,"timeout_ms":null}`,
		`{"command":"printf direct-result","execution_mode":"foreground","timeout_ms":10000}`,
		`{"command":"printf direct-result","run_in_background":false,"lifetime":"job","timeout":10000}`,
	} {
		result, err := adapted.Run(context.Background(), ai.ToolCall{ID: "foreground", Name: "Bash", Input: input})
		if err != nil || agent.ResultText(result, err) != "direct-result" {
			t.Fatalf("foreground did not return its result directly: result=%+v err=%v", result, err)
		}
		details, ok := result.Details.(toolresult.BashDetails)
		if !ok || details.TaskID != "" || len(task.Default().List()) != 0 {
			t.Fatalf("foreground created a background task: %+v", result)
		}
	}
	for _, params := range []map[string]any{nil, {"command": ""}} {
		if result := worker.ExecuteApproved(context.Background(), params, t.TempDir()); result.Success || !strings.Contains(result.Error, "command is required") {
			t.Fatalf("missing command was executed: %+v", result)
		}
	}
}

func TestShellCanonicalModesMatchApprovalAndRuntime(t *testing.T) {
	task.Initialize(task.Options{})
	t.Cleanup(task.ResetDefaultTracker)
	t.Cleanup(func() {
		for _, worker := range task.Default().ListRunning() {
			_ = task.Default().Kill(worker.GetID())
		}
	})
	worker := bashTool(t)
	var service task.BackgroundTask
	for _, mode := range []string{shellBackgroundService, shellBackgroundJob} {
		params := map[string]any{"command": "exec sleep 60", "execution_mode": mode, "timeout_ms": nil}
		if mode == shellBackgroundJob {
			params["timeout_ms"] = 1000
		}
		request, err := worker.PreparePermission(context.Background(), params, t.TempDir())
		if err != nil || !request.BashMeta.RunBackground {
			t.Fatalf("approval misrepresented mode %s: request=%+v err=%v", mode, request, err)
		}
		result := worker.ExecuteApproved(context.Background(), params, t.TempDir())
		if !result.Success {
			t.Fatalf("launch mode %s: %s", mode, result.Error)
		}
		id := result.Details.(toolresult.BashDetails).TaskID
		background, ok := task.Default().Get(id)
		if !ok {
			t.Fatalf("no registered task for mode %s", mode)
		}
		if mode == shellBackgroundService {
			service = background
			if info := service.GetStatus(); info.Lifetime != task.LifetimeService || !strings.Contains(result.Output, "Startup is not health confirmation") {
				t.Fatalf("service lost lifetime or readiness boundary: %+v / %s", info, result.Output)
			}
			continue
		}
		if !background.WaitForCompletion(5 * time.Second) {
			t.Fatal("finite job did not enforce timeout_ms")
		}
		if info := background.GetStatus(); info.Status != task.StatusFailed || info.Failure.Kind != task.FailureTimeout || info.Failure.Timeout != time.Second {
			t.Fatalf("finite job lost deadline evidence: %+v", info)
		}
		if !service.IsRunning() {
			t.Fatal("service inherited the finite job deadline")
		}
	}
	if err := task.Default().StopServices(""); err != nil {
		t.Fatal(err)
	}
	if status := service.GetStatus().Status; status != task.StatusStopped {
		t.Fatalf("service cleanup became a failure: %s", status)
	}
	foreground, err := worker.PreparePermission(context.Background(), map[string]any{"command": "exit 0", "execution_mode": shellForeground}, t.TempDir())
	if err != nil || foreground.BashMeta.RunBackground {
		t.Fatalf("foreground approval was marked background: %+v / %v", foreground, err)
	}
}

func TestShellCanonicalJobCompletion(t *testing.T) {
	task.Initialize(task.Options{})
	t.Cleanup(task.ResetDefaultTracker)
	worker := bashTool(t)
	for _, tc := range []struct {
		mode   string
		status task.TaskStatus
	}{
		{shellBackgroundJob, task.StatusCompleted},
		{shellBackgroundService, task.StatusFailed},
	} {
		result := worker.ExecuteApproved(context.Background(), map[string]any{"command": "exit 0", "execution_mode": tc.mode}, t.TempDir())
		if !result.Success {
			t.Fatal(result.Error)
		}
		id := result.Details.(toolresult.BashDetails).TaskID
		background, ok := task.Default().Get(id)
		if !ok || !background.WaitForCompletion(5*time.Second) {
			t.Fatal("background command did not settle")
		}
		if info := background.GetStatus(); info.Status != tc.status {
			t.Fatalf("mode %s: exit 0 status=%s want %s", tc.mode, info.Status, tc.status)
		}
	}
}
