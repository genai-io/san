package task

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAgentActivitySnapshotBoundedAndReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task.log")
	worker := NewAgentTask("activity", "", "", context.Background(), nil, path)
	for i := range 15 {
		worker.AppendProgress(fmt.Sprintf(" action %d ", i))
	}
	for _, msg := range []string{"", "  ", "Model: worker", "Mode: explore", "Usage: tokens"} {
		worker.AppendProgress(msg)
	}
	info := worker.GetStatus()
	if len(info.Activity) != 12 || info.Activity[0] != "action 3" || info.Activity[11] != "action 14" || info.Progress != "action 14" {
		t.Fatalf("wrong activity: %+v", info)
	}
	info.Activity[0] = "mutated"
	worker.AppendProgress("next")
	if info.Activity[11] != "action 14" || worker.GetStatus().Activity[0] != "action 4" {
		t.Fatal("snapshot aliases task activity")
	}
	large := strings.Repeat("界", maxActivityEntryBytes)
	worker.AppendProgress(large)
	info = worker.GetStatus()
	last := info.Activity[len(info.Activity)-1]
	if len(last) > maxActivityEntryBytes || !utf8.ValidString(last) || info.Progress != large {
		t.Fatal("entry limit changed latest progress or split UTF-8")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), large) || !strings.Contains(string(data), "Model: worker") {
		t.Fatal("display filtering or truncation changed progress log")
	}
	worker.Complete(nil)
	if _, err := worker.BeginAttempt("", context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if next := worker.GetStatus(); len(next.Activity) != 0 || next.Progress != "" {
		t.Fatalf("activity survived attempt: %+v", next)
	}
	if len(info.Activity) != 12 || info.Activity[11] != last {
		t.Fatal("attempt reset changed old snapshot")
	}
}
