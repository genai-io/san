package task

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemovedOutputLogIsNotRecreatedAsComplete(t *testing.T) {
	for _, kind := range []TaskType{TaskTypeAgent, TaskTypeBash} {
		for _, phase := range []string{"append", "completion"} {
			t.Run(string(kind)+"/"+phase, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "output.jsonl")
				var worker BackgroundTask
				if kind == TaskTypeAgent {
					worker = NewAgentTask("test", "reviewer", "Review", context.Background(), nil, path)
				} else {
					worker = NewBashTask("test", "true", "Test", &exec.Cmd{Process: &os.Process{Pid: 123}}, nil, path)
				}
				worker.AppendOutput([]byte("EARLIER_FINDINGS"))
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if phase == "append" {
					worker.AppendOutput([]byte("LATER_FINDINGS"))
				}
				switch w := worker.(type) {
				case *AgentTask:
					w.Complete(nil)
				case *BashTask:
					w.Complete(0, nil)
				}
				info := worker.GetStatus()
				if info.OutputError == "" || info.OutputFile != "" || info.Status != StatusCompleted || !strings.Contains(info.Output, "EARLIER_FINDINGS") {
					t.Fatalf("removed log advertised as complete or lost in-memory output: %+v", info)
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("removed log was recreated: %v", err)
				}
			})
		}
	}
}
