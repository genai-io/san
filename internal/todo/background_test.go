package todo

import (
	"testing"

	"github.com/genai-io/san/internal/task"
)

func metadataStr(metadata map[string]any, key string) string {
	if v, ok := metadata[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func TestTrackWorkerCreatesEntry(t *testing.T) {
	Initialize(Options{})
	t.Cleanup(func() { Default().Reset() })

	TrackWorker(Default(), task.TaskInfo{
		ID:          "bg-1",
		Type:        task.TaskTypeAgent,
		AgentName:   "dir-audit",
		AgentType:   "Explore",
		Description: "Directory structure audit",
	})

	items := Default().List()
	if len(items) != 1 {
		t.Fatalf("expected 1 tracker item, got %d", len(items))
	}
	if items[0].Status != StatusInProgress {
		t.Fatalf("status = %q, want %q", items[0].Status, StatusInProgress)
	}
	if metadataStr(items[0].Metadata, metaTaskID) != "bg-1" {
		t.Fatalf("item ID metadata = %q", metadataStr(items[0].Metadata, metaTaskID))
	}
}

// A bash worker names no agent, so the subject falls back through description
// to the command itself. CompleteWorker already fired for bash items before
// they were tracked at all; now that both halves run, they need a readable row.
func TestTrackWorkerNamesBashWorker(t *testing.T) {
	Initialize(Options{})
	t.Cleanup(func() { Default().Reset() })

	TrackWorker(Default(), task.TaskInfo{
		ID:      "bg-2",
		Type:    task.TaskTypeBash,
		Command: "go test ./...",
	})

	items := Default().List()
	if len(items) != 1 {
		t.Fatalf("expected 1 tracker item, got %d", len(items))
	}
	if items[0].Subject != "go test ./..." {
		t.Fatalf("subject = %q, want the command", items[0].Subject)
	}
}

func TestTrackWorkerIgnoresTaskWithoutID(t *testing.T) {
	Initialize(Options{})
	t.Cleanup(func() { Default().Reset() })

	TrackWorker(Default(), task.TaskInfo{Description: "no worker behind this"})

	if items := Default().List(); len(items) != 0 {
		t.Fatalf("expected no tracker item, got %d", len(items))
	}
}

func TestCompleteWorkerUpdatesStatus(t *testing.T) {
	Initialize(Options{})
	t.Cleanup(func() { Default().Reset() })

	TrackWorker(Default(), task.TaskInfo{
		ID:          "bg-1",
		Type:        task.TaskTypeAgent,
		AgentName:   "dir-audit",
		AgentType:   "Explore",
		Description: "Directory audit",
	})

	CompleteWorker(Default(), task.TaskInfo{
		ID:     "bg-1",
		Type:   task.TaskTypeAgent,
		Status: task.StatusCompleted,
	})

	items := Default().List()
	if len(items) != 1 {
		t.Fatalf("expected 1 tracker item, got %d", len(items))
	}
	if items[0].Status != StatusCompleted {
		t.Fatalf("status = %q, want %q", items[0].Status, StatusCompleted)
	}
	if metadataStr(items[0].Metadata, metaStatusDetail) != string(task.StatusCompleted) {
		t.Fatalf("status detail = %q", metadataStr(items[0].Metadata, metaStatusDetail))
	}
}

func TestCompleteWorkerTracksFailure(t *testing.T) {
	Initialize(Options{})
	t.Cleanup(func() { Default().Reset() })

	TrackWorker(Default(), task.TaskInfo{
		ID:          "bg-1",
		Type:        task.TaskTypeAgent,
		AgentName:   "fix-auth",
		AgentType:   "subagent",
		Description: "Fix auth module",
	})

	CompleteWorker(Default(), task.TaskInfo{
		ID:     "bg-1",
		Type:   task.TaskTypeAgent,
		Status: task.StatusFailed,
		Error:  "compilation error",
	})

	items := Default().List()
	if metadataStr(items[0].Metadata, metaStatusDetail) != string(task.StatusFailed) {
		t.Fatalf("status detail = %q, want %q", metadataStr(items[0].Metadata, metaStatusDetail), task.StatusFailed)
	}
}
