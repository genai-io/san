package task

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestTaskOutputLogFailureIsSeparateFromOutcome(t *testing.T) {
	for _, kind := range []TaskType{TaskTypeAgent, TaskTypeBash} {
		for _, phase := range []string{"init", "append", "completion"} {
			t.Run(string(kind)+"/"+phase, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "output.log")
				if phase == "init" {
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				var worker BackgroundTask
				if kind == TaskTypeAgent {
					worker = NewAgentTask("test", "review", "Review", context.Background(), nil, path)
				} else {
					worker = NewBashTask("test", "true", "Test", &exec.Cmd{Process: &os.Process{Pid: 123}}, nil, path)
				}
				if phase != "init" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if phase == "append" {
						if err := os.Mkdir(path, 0o700); err != nil {
							t.Fatal(err)
						}
					}
				}
				worker.AppendOutput([]byte("REPORT"))
				if phase == "completion" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				switch w := worker.(type) {
				case *AgentTask:
					w.Complete(nil)
				case *BashTask:
					w.Complete(0, nil)
				}
				info := worker.GetStatus()
				if info.Status != StatusCompleted || info.Error != "" || info.OutputError == "" || info.OutputFile != "" || !strings.Contains(info.Output, "REPORT") {
					t.Fatalf("log failure was hidden or changed execution outcome: %+v", info)
				}
			})
		}
	}
}

func TestBackgroundProgressDoesNotPolluteResult(t *testing.T) {
	worker := NewAgentTask("test", "review", "Review", context.Background(), nil, "")
	worker.AppendProgress("Read(source.go)")
	for _, line := range []string{"Model: worker", "Mode: explore", "Usage: input=30 output=4"} {
		worker.AppendProgress(line)
	}
	if info := worker.GetStatus(); info.Progress != "Read(source.go)" || info.Output != "" {
		t.Fatalf("wrong progress/result: %+v", info)
	}
	var writers sync.WaitGroup
	for range 4 {
		writers.Go(func() {
			for range 20 {
				worker.AppendProgress("Read(next.go)")
				worker.GetStatus()
			}
		})
	}
	writers.Wait()
	worker.AppendOutput([]byte("final findings"))
	if worker.GetOutput() != "final findings" {
		t.Fatal("display-only progress leaked into final result")
	}
}
