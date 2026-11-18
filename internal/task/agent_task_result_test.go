package task

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAgentTaskSetResultReplacesPreviewAndPreservesFullLog(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "output.jsonl")
	task := NewAgentTask("result", "flow", "result", ctx, cancel, path)
	task.AppendOutput([]byte("unfinished findings"))
	result := "Conclusion\n" + strings.Repeat("界", maxOutputBufferSize/3+10) + "final trailing evidence"
	task.SetResult(result)
	snapshot := task.GetStatus()
	if snapshot.Result != result || snapshot.Output != task.GetOutput() {
		t.Fatal("snapshot did not retain full result independently of bounded output")
	}
	got := task.GetOutput()
	if !strings.HasPrefix(got, "Conclusion\n") || strings.Contains(got, "unfinished") || len(got) > maxOutputBufferSize || len(got) < maxOutputBufferSize-3 || !utf8.ValidString(got) {
		t.Fatalf("result head invalid: bytes=%d valid=%v", len(got), utf8.ValidString(got))
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	var contents []string
	for decoder.More() {
		var record struct {
			Event   string `json:"event"`
			Content string `json:"content"`
		}
		if err := decoder.Decode(&record); err != nil {
			t.Fatal(err)
		}
		if record.Event == "task.output" {
			contents = append(contents, record.Content)
		}
	}
	if len(contents) != 2 || contents[0] != "unfinished findings" || contents[1] != result {
		t.Fatalf("full output records not retained: count=%d", len(contents))
	}
	task.SetResult("short answer")
	if got := task.GetOutput(); got != "short answer" {
		t.Fatalf("short replacement = %q", got)
	}
	if got := task.GetStatus().Result; got != "short answer" {
		t.Fatalf("short result replacement = %q", got)
	}
	task.SetResult("")
	if task.GetStatus().Result != "" || snapshot.Result != result {
		t.Fatal("empty result did not replace prior value or mutated an existing snapshot")
	}
	if got := task.GetOutput(); got != "" {
		t.Fatalf("empty replacement = %q", got)
	}
}

func TestAgentTaskResultIndependentOfOutputLog(t *testing.T) {
	for _, path := range []string{"", t.TempDir()} {
		t.Run(path, func(t *testing.T) {
			task := NewAgentTask("result", "flow", "result", context.Background(), nil, path)
			result := strings.Repeat("full report\n", maxOutputBufferSize) + "final evidence"
			task.SetResult(result)
			task.AppendOutput([]byte(strings.Repeat("x", maxOutputBufferSize) + "tail"))
			task.Complete(nil)
			info := task.GetStatus()
			if info.Status != StatusCompleted || info.Result != result || len(info.Output) != maxOutputBufferSize || !strings.HasSuffix(info.Output, "tail") {
				t.Fatal("full result depends on log availability or bounded output")
			}
			if path != "" && info.OutputError == "" {
				t.Fatal("expected unavailable log error")
			}
		})
	}
}

func TestAgentTaskResultDisplayedMetadata(t *testing.T) {
	a := NewAgentTask("main-result", "flow", "work", context.Background(), nil, "")
	a.SetResult("native answer")
	a.MarkResultDisplayed()
	snapshot := a.GetStatus()
	if !snapshot.ResultDisplayed || snapshot.Result != "native answer" {
		t.Fatal("displayed metadata lost the retained native result")
	}
	a.SetResult("replacement")
	if a.GetStatus().ResultDisplayed || !snapshot.ResultDisplayed {
		t.Fatal("replacement retained display flag or mutated prior snapshot")
	}
}

func TestAgentTaskAppendOutputStillRetainsTail(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	task := NewAgentTask("append", "flow", "append", ctx, cancel, "")
	task.AppendOutput([]byte("old output"))
	task.AppendOutput([]byte(strings.Repeat("x", maxOutputBufferSize) + "tail"))
	got := task.GetOutput()
	if len(got) != maxOutputBufferSize || !strings.HasSuffix(got, "tail") || strings.Contains(got, "old output") {
		t.Fatalf("append tail contract changed: bytes=%d", len(got))
	}
}
