package fs

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/genai-io/san/internal/task"
)

func TestShellOutputStaysBoundedWhileCountingFullStream(t *testing.T) {
	var out shellOutput
	chunk := []byte(strings.Repeat("中文\n", 20000))
	for range 10 {
		if n, err := out.Write(chunk); err != nil || n != len(chunk) {
			t.Fatalf("write = %d, %v", n, err)
		}
	}
	if len(out.Bytes()) > maxShellOutputBytes+1 || out.total != int64(len(chunk)*10) || out.LineCount() != 200000 {
		t.Fatalf("retained = %d, total = %d, lines = %d", len(out.Bytes()), out.total, out.LineCount())
	}
	if got := truncateShellOutput(out.Decoded()); !utf8.ValidString(got) || !strings.Contains(got, "truncated") {
		t.Fatalf("bad truncated UTF-8: %q", got[len(got)-40:])
	}
}

func TestLargeForegroundOutputAlsoCapsHooks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "large.txt"), []byte(strings.Repeat("中文\n", 200000)), 0o600); err != nil {
		t.Fatal(err)
	}
	result := bashTool(t).ExecuteApproved(context.Background(), map[string]any{
		"command": "cat large.txt; cat large.txt >&2", "timeout": 10000,
	}, dir)
	if !result.Success {
		t.Fatal(result.Error)
	}
	if len(result.Output) > maxShellOutputBytes+40 || !utf8.ValidString(result.Output) || result.Metadata.LineCount != 400000 {
		t.Fatalf("retained = %d, valid UTF-8 = %v, lines = %d", len(result.Output), utf8.ValidString(result.Output), result.Metadata.LineCount)
	}
	for _, stream := range []string{"stdout", "stderr"} {
		text := result.HookResponse.(map[string]any)[stream].(string)
		if len(text) > maxShellOutputBytes+40 || !utf8.ValidString(text) || !strings.Contains(text, "truncated") {
			t.Fatalf("%s hook = %d bytes", stream, len(text))
		}
	}
}

func TestLargeBackgroundOutputKeepsFullDiskLog(t *testing.T) {
	dir := t.TempDir()
	if err := task.Default().SetOutputDir(filepath.Join(dir, "logs")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = task.Default().SetOutputDir("") })
	want := strings.Repeat("中文\n", 200000) + "finished\n"
	if err := os.WriteFile(filepath.Join(dir, "large.txt"), []byte(strings.TrimSuffix(want, "finished\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	result := bashTool(t).ExecuteApproved(context.Background(), map[string]any{
		"command": "cat large.txt; printf 'finished\\n' >&2", "run_in_background": true, "timeout": 10000,
	}, dir)
	if !result.Success {
		t.Fatal(result.Error)
	}
	id := result.HookResponse.(map[string]any)["backgroundTask"].(map[string]any)["taskId"].(string)
	job, ok := task.Default().Get(id)
	if !ok || !job.WaitForCompletion(15*time.Second) {
		t.Fatal("background output did not finish")
	}
	if status := job.GetStatus(); status.Status != task.StatusCompleted {
		t.Fatalf("background status = %+v", status)
	}
	if out := job.GetOutput(); len(out) > 512*1024 || !strings.HasSuffix(out, "finished\n") {
		t.Fatalf("preview = %d bytes", len(out))
	}
	f, err := os.Open(job.GetStatus().OutputFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var full strings.Builder
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var record struct{ Event, Content string }
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		if record.Event == "task.output" {
			full.WriteString(record.Content)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if full.String() != want {
		t.Fatalf("disk output length = %d, want %d", full.Len(), len(want))
	}
}
