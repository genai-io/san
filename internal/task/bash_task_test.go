package task

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"testing"
	"time"
)

func TestBashTask_Complete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "echo", "test")
	cmd.Start()

	task := NewBashTask("test-id", "echo test", "Test task", cmd, ctx, cancel)

	// Complete the task
	task.Complete(0, nil)

	info := task.GetStatus()
	if info.Status != StatusCompleted {
		t.Errorf("expected status 'completed', got '%s'", info.Status)
	}
	if info.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", info.ExitCode)
	}
}

func TestBashTask_Failed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "echo", "test")
	cmd.Start()

	task := NewBashTask("fail-id", "exit 1", "Failing task", cmd, ctx, cancel)

	// Complete with non-zero exit code
	task.Complete(1, nil)

	info := task.GetStatus()
	if info.Status != StatusFailed {
		t.Errorf("expected status 'failed', got '%s'", info.Status)
	}
	if info.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %d", info.ExitCode)
	}
}

func TestBashTask_MarkKilled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "echo", "test")
	cmd.Start()

	task := NewBashTask("kill-id", "sleep 100", "Long task", cmd, ctx, cancel)

	task.markKilled()

	info := task.GetStatus()
	if info.Status != StatusKilled {
		t.Errorf("expected status 'killed', got '%s'", info.Status)
	}
}

func TestBashTask_AppendAndGetOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "echo", "test")
	cmd.Start()

	task := NewBashTask("output-id", "echo test", "Output task", cmd, ctx, cancel)

	task.AppendOutput([]byte("line 1\n"))
	task.AppendOutput([]byte("line 2\n"))

	output := task.GetOutput()
	expected := "line 1\nline 2\n"
	if output != expected {
		t.Errorf("expected output '%s', got '%s'", expected, output)
	}
}

func TestBashTask_IsRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "echo", "test")
	cmd.Start()

	task := NewBashTask("running-id", "echo test", "Running task", cmd, ctx, cancel)

	if !task.IsRunning() {
		t.Error("task should be running initially")
	}

	task.Complete(0, nil)

	if task.IsRunning() {
		t.Error("task should not be running after completion")
	}
}

func TestBashTask_WaitForCompletion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "echo", "test")
	cmd.Start()

	task := NewBashTask("wait-id", "echo test", "Wait task", cmd, ctx, cancel)

	// Complete in background after short delay
	go func() {
		time.Sleep(200 * time.Millisecond)
		task.Complete(0, nil)
	}()

	completed := task.WaitForCompletion(time.Second)
	if !completed {
		t.Error("expected task to complete within timeout")
	}
}

func TestBashTask_WaitForCompletionTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "echo", "test")
	cmd.Start()

	task := NewBashTask("timeout-id", "sleep 100", "Timeout task", cmd, ctx, cancel)

	// Don't complete the task, let it timeout
	completed := task.WaitForCompletion(200 * time.Millisecond)
	if completed {
		t.Error("expected timeout, but task completed")
	}
}

func TestBashTask_GetStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "echo", "test")
	cmd.Start()

	task := NewBashTask("status-id", "echo test", "Status task", cmd, ctx, cancel)
	task.AppendOutput([]byte("output\n"))

	info := task.GetStatus()

	if info.ID != "status-id" {
		t.Errorf("expected ID 'status-id', got '%s'", info.ID)
	}
	if info.Type != TaskTypeBash {
		t.Errorf("expected type 'bash', got '%s'", info.Type)
	}
	if info.Command != "echo test" {
		t.Errorf("expected command 'echo test', got '%s'", info.Command)
	}
	if info.Status != StatusRunning {
		t.Errorf("expected status Running, got '%s'", info.Status)
	}
	if info.Output != "output\n" {
		t.Errorf("expected output 'output\\n', got '%s'", info.Output)
	}
}

func TestBashTask_ConcurrentAccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "echo", "test")
	cmd.Start()

	task := NewBashTask("concurrent-id", "echo test", "Concurrent task", cmd, ctx, cancel)

	var wg sync.WaitGroup

	// Multiple goroutines reading and writing
	for i := 0; i < 10; i++ {
		wg.Add(3)

		go func() {
			defer wg.Done()
			task.AppendOutput([]byte("data\n"))
		}()

		go func() {
			defer wg.Done()
			_ = task.GetOutput()
		}()

		go func() {
			defer wg.Done()
			_ = task.IsRunning()
		}()
	}

	wg.Wait()

	// Complete should not panic with concurrent access
	task.Complete(0, nil)
}

func TestBashTask_StatusRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "echo", "test")
	cmd.Start()

	task := NewBashTask("running-status-id", "echo test", "Running status task", cmd, ctx, cancel)

	// Newly created task should be in Running state
	info := task.GetStatus()
	if info.Status != StatusRunning {
		t.Errorf("expected initial status %q, got %q", StatusRunning, info.Status)
	}

	// IsRunning should also confirm
	if !task.IsRunning() {
		t.Error("IsRunning() should be true for new task")
	}
}

func TestBashTask_AllStateTransitions(t *testing.T) {
	// Running -> Completed
	t.Run("Running to Completed", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cmd := exec.CommandContext(ctx, "echo", "test")
		cmd.Start()
		task := NewBashTask("t1", "echo test", "test", cmd, ctx, cancel)
		if info := task.GetStatus(); info.Status != StatusRunning {
			t.Errorf("want %s, got %s", StatusRunning, info.Status)
		}
		task.Complete(0, nil)
		if info := task.GetStatus(); info.Status != StatusCompleted {
			t.Errorf("want %s, got %s", StatusCompleted, info.Status)
		}
	})

	// Running -> Failed (non-zero exit)
	t.Run("Running to Failed", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cmd := exec.CommandContext(ctx, "echo", "test")
		cmd.Start()
		task := NewBashTask("t2", "exit 1", "test", cmd, ctx, cancel)
		task.Complete(2, nil)
		if info := task.GetStatus(); info.Status != StatusFailed {
			t.Errorf("want %s, got %s", StatusFailed, info.Status)
		}
	})

	// Running -> Killed
	t.Run("Running to Killed", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cmd := exec.CommandContext(ctx, "echo", "test")
		cmd.Start()
		task := NewBashTask("t3", "sleep 100", "test", cmd, ctx, cancel)
		task.markKilled()
		if info := task.GetStatus(); info.Status != StatusKilled {
			t.Errorf("want %s, got %s", StatusKilled, info.Status)
		}
	})
}

func TestBashTask_ImplementsBackgroundTask(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "echo", "test")
	cmd.Start()

	task := NewBashTask("interface-id", "echo test", "Interface test", cmd, ctx, cancel)

	// Test that it implements BackgroundTask
	var bt BackgroundTask = task

	if bt.GetID() != "interface-id" {
		t.Errorf("GetID() = %s, want interface-id", bt.GetID())
	}
	if bt.GetType() != TaskTypeBash {
		t.Errorf("GetType() = %s, want bash", bt.GetType())
	}
	if bt.GetDescription() != "Interface test" {
		t.Errorf("GetDescription() = %s, want 'Interface test'", bt.GetDescription())
	}
}

// A user-requested stop cancels the task context, and the child dies of the
// signal that follows — so cmd.Wait reports "signal: killed", not a
// cancellation. Recording that as failed told the main agent the work had
// broken rather than been called off, inviting a retry of what the user had
// just cancelled.
func TestBashTaskStopIsNotAFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "echo", "test")
	cmd.Start()

	task := NewBashTask("stop-id", "sleep 100", "Long task", cmd, ctx, cancel)

	cancel()
	task.Complete(137, errors.New("signal: killed"))

	if info := task.GetStatus(); info.Status != StatusStopped {
		t.Errorf("expected status '%s', got '%s'", StatusStopped, info.Status)
	}
}

// A run that outlives its own timeout also ends with a cancelled context, but
// it failed on its own terms — nobody called it off, so it stays a failure.
func TestBashTaskTimeoutRemainsAFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()

	cmd := exec.Command("echo", "test")
	cmd.Start()

	task := NewBashTask("timeout-id", "sleep 100", "Long task", cmd, ctx, cancel)

	<-ctx.Done()
	task.Complete(137, errors.New("signal: killed"))

	if info := task.GetStatus(); info.Status != StatusFailed {
		t.Errorf("expected status '%s', got '%s'", StatusFailed, info.Status)
	}
}
