package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/genai-io/san/internal/task"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestBashFailurePreservesStructuredDisplayDetails(t *testing.T) {
	result := (&BashTool{}).ExecuteApproved(context.Background(), map[string]any{
		"command": "printf 'line one\\nline two\\n'; printf 'stderr\\n' >&2; exit 7",
	}, t.TempDir())
	if result.Success {
		t.Fatal("ExecuteApproved() succeeded, want exit failure")
	}
	if result.Error != "exit code 7" {
		t.Fatalf("error = %q, want exit code 7", result.Error)
	}
	if got := result.FormatForLLM(); got != "line one\nline two\nstderr\n\nError: exit code 7" {
		t.Fatalf("FormatForLLM() = %q, want stdout and stderr joined without an extra blank output line", got)
	}
	details, ok := result.Details.(toolresult.BashDetails)
	if !ok {
		t.Fatalf("details = %#v, want BashDetails", result.Details)
	}
	if details.Error != "exit code 7" || details.LineCount != 3 {
		t.Fatalf("details = %#v, want exit code 7 and 3 output lines", details)
	}
}

func TestBashFailureWithoutOutputHasZeroDisplayLines(t *testing.T) {
	result := (&BashTool{}).ExecuteApproved(context.Background(), map[string]any{
		"command": "exit 3",
	}, t.TempDir())
	if result.Success {
		t.Fatal("ExecuteApproved() succeeded, want exit failure")
	}
	details, ok := result.Details.(toolresult.BashDetails)
	if !ok || details.Error != "exit code 3" || details.LineCount != 0 {
		t.Fatalf("details = %#v, want exit code 3 and no output lines", result.Details)
	}
	if got := result.FormatForLLM(); got != "Error: exit code 3" {
		t.Fatalf("FormatForLLM() = %q, want unchanged error text", got)
	}
}

func TestBashToolTracksChangedDirectory(t *testing.T) {
	cwd := t.TempDir()
	subdir := filepath.Join(cwd, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	result := (&BashTool{}).ExecuteApproved(context.Background(), map[string]any{
		"command": "cd subdir",
	}, cwd)
	if !result.Success {
		t.Fatalf("ExecuteApproved() failed: %s", result.Error)
	}

	resp, ok := result.HookResponse.(map[string]any)
	if !ok {
		t.Fatalf("expected hook response map, got %#v", result.HookResponse)
	}
	got, _ := resp["cwd"].(string)
	gotResolved, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(got) error = %v", err)
	}
	wantResolved, err := filepath.EvalSymlinks(subdir)
	if err != nil {
		t.Fatalf("EvalSymlinks(subdir) error = %v", err)
	}
	if gotResolved != wantResolved {
		t.Fatalf("tracked cwd = %q (%q), want %q (%q)", got, gotResolved, subdir, wantResolved)
	}
}

func TestBackgroundBashReportsProcessGroupStopCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not provide Bash process-group control")
	}
	task.Initialize(task.Options{})
	t.Cleanup(task.ResetDefaultTracker)
	t.Cleanup(func() {
		for _, bgTask := range task.Default().ListRunning() {
			_ = task.Default().Kill(bgTask.GetID())
		}
	})

	result := (&BashTool{}).ExecuteApproved(context.Background(), map[string]any{
		"command":           "sleep 60",
		"run_in_background": true,
	}, t.TempDir())
	if !result.Success {
		t.Fatalf("background Bash failed: %s", result.Error)
	}
	for _, want := range []string{
		"Process group ID:",
		"kill -TERM -- -",
		"kill -KILL -- -",
	} {
		if !strings.Contains(result.Output, want) {
			t.Errorf("background result missing %q:\n%s", want, result.Output)
		}
	}

	response, ok := result.HookResponse.(map[string]any)
	if !ok {
		t.Fatalf("hook response = %#v, want map", result.HookResponse)
	}
	background, ok := response["backgroundTask"].(map[string]any)
	if !ok || background["processGroupId"] == nil {
		t.Fatalf("background task metadata = %#v, want processGroupId", background)
	}

	bgTask, ok := task.Default().Get(background["taskId"].(string))
	if !ok {
		t.Fatalf("task %v not registered", background["taskId"])
	}
	stop := (&BashTool{}).ExecuteApproved(context.Background(), map[string]any{
		"command": fmt.Sprintf("kill -TERM -- -%d", background["processGroupId"]),
	}, t.TempDir())
	if !stop.Success {
		t.Fatalf("graceful process-group stop failed: %s", stop.Error)
	}
	if !bgTask.WaitForCompletion(time.Second) {
		t.Fatal("background task did not stop after its reported Bash command")
	}
	if got := bgTask.GetStatus().Status; got != task.StatusStopped {
		t.Fatalf("background task status = %s, want %s", got, task.StatusStopped)
	}
}
