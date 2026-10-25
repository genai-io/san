package task

import (
	"context"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestBashFailureEvidence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix signals")
	}
	for _, tc := range []struct {
		name, command string
		deadline      bool
		kind          FailureKind
		code          int
	}{
		{"finite", "exit 7", false, FailureExit, 7},
		{"external kill", "kill -KILL $$", false, FailureSignal, 137},
		{"deadline", "exec sleep 5", true, FailureTimeout, 137},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			if tc.deadline {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
			}
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", "-c", tc.command)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			task := NewBashTask("test", tc.command, "", cmd, cancel, "")
			err := cmd.Wait()
			contextErr := ctx.Err()
			task.MarkReaped()
			task.CompleteWithContext(0, err, contextErr, 30*time.Millisecond)
			info := task.GetStatus()
			if info.Status != StatusFailed || info.ExitCode != tc.code || info.Failure.Kind != tc.kind {
				t.Fatalf("unexpected result: %+v", info)
			}
			if tc.name == "external kill" && (info.Failure.Timeout != 0 || info.Failure.Signal == "" || info.Error == "") {
				t.Fatalf("lost raw signal evidence: %+v", info)
			}
		})
	}
}

func TestServiceExitAndStop(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		cmd := exec.Command("go", "version")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		task := NewBashTask("test", "go version", "", cmd, nil, "")
		task.SetLifetime(LifetimeService)
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
		task.MarkReaped()
		if stopped {
			if err := task.Stop(); err != nil {
				t.Fatal(err)
			}
		}
		task.Complete(0, nil)
		want := StatusFailed
		if stopped {
			want = StatusStopped
		}
		if info := task.GetStatus(); info.Status != want || info.Lifetime != LifetimeService {
			t.Fatalf("unexpected: %+v", info)
		}
	}
}
