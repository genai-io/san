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
	task.SetResult("")
	if got := task.GetOutput(); got != "" {
		t.Fatalf("empty replacement = %q", got)
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
