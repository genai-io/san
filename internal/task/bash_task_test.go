package task

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestBashTask_Complete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "echo", "test")
	cmd.Start()

	task := NewBashTask("test-id", "echo test", "Test task", cmd, cancel)

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

	task := NewBashTask("fail-id", "exit 1", "Failing task", cmd, cancel)

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

	task := NewBashTask("kill-id", "sleep 100", "Long task", cmd, cancel)

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

	task := NewBashTask("output-id", "echo test", "Output task", cmd, cancel)

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

	task := NewBashTask("running-id", "echo test", "Running task", cmd, cancel)

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

	task := NewBashTask("wait-id", "echo test", "Wait task", cmd, cancel)

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

	task := NewBashTask("timeout-id", "sleep 100", "Timeout task", cmd, cancel)

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

	task := NewBashTask("status-id", "echo test", "Status task", cmd, cancel)
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

	task := NewBashTask("concurrent-id", "echo test", "Concurrent task", cmd, cancel)

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

	task := NewBashTask("running-status-id", "echo test", "Running status task", cmd, cancel)

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
		task := NewBashTask("t1", "echo test", "test", cmd, cancel)
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
		task := NewBashTask("t2", "exit 1", "test", cmd, cancel)
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
		task := NewBashTask("t3", "sleep 100", "test", cmd, cancel)
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

	task := NewBashTask("interface-id", "echo test", "Interface test", cmd, cancel)

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

// A stopped child dies of the signal Stop sent it, so cmd.Wait reports
// "signal: terminated" — indistinguishable from a crash by the error alone.
// Recording that as failed told the main agent the work had broken rather than
// been called off, inviting a retry of what the user had just cancelled.
func TestBashTaskStopIsNotAFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "echo", "test")
	cmd.Start()

	task := NewBashTask("stop-id", "sleep 100", "Long task", cmd, cancel)

	_ = task.Stop()
	task.Complete(signalExitCode(syscall.SIGTERM), errors.New("signal: terminated"))

	if info := task.GetStatus(); info.Status != StatusStopped {
		t.Errorf("expected status '%s', got '%s'", StatusStopped, info.Status)
	}
}

// A run killed by its own timeout dies the same way, but nobody called it off,
// so it stays a failure. Only Stop exempts a task from that.
func TestBashTaskTimeoutRemainsAFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()

	cmd := exec.Command("echo", "test")
	cmd.Start()

	task := NewBashTask("timeout-id", "sleep 100", "Long task", cmd, cancel)

	<-ctx.Done()
	task.Complete(signalExitCode(syscall.SIGKILL), errors.New("signal: killed"))

	if info := task.GetStatus(); info.Status != StatusFailed {
		t.Errorf("expected status '%s', got '%s'", StatusFailed, info.Status)
	}
}

// Stop must not cancel the run's context: exec wires cmd.Cancel to SIGKILL the
// group the moment that context is done, which would land before the SIGTERM
// and make the graceful stop a lie.
func TestBashTaskStopLeavesContextLive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "echo", "test")
	cmd.Start()

	task := NewBashTask("graceful-id", "sleep 100", "Long task", cmd, cancel)

	_ = task.Stop()

	if err := ctx.Err(); err != nil {
		t.Errorf("Stop cancelled the task context (%v); the SIGKILL that follows "+
			"cancellation would pre-empt the SIGTERM Stop just sent", err)
	}
}

// A background command that never stops talking used to have every byte it
// ever printed held in memory for the life of the process: BashTask skipped
// the cap AgentTask applied, and the manager never forgets a task.
func TestBashTaskOutputIsCapped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "echo", "test")
	cmd.Start()

	task := NewBashTask("cap-id", "chatty", "Chatty task", cmd, cancel)

	for range 4 {
		task.AppendOutput(bytes.Repeat([]byte("x"), maxOutputBufferSize/2))
	}
	task.AppendOutput([]byte("TAIL"))

	output := task.GetOutput()
	if len(output) > maxOutputBufferSize {
		t.Fatalf("output buffer = %d bytes, want <= %d", len(output), maxOutputBufferSize)
	}
	// The tail is the part worth keeping — it is where a failure shows up.
	if !strings.HasSuffix(output, "TAIL") {
		t.Error("cap discarded the newest output instead of the oldest")
	}
}
