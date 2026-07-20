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
		AgentType:   "general-purpose",
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

// The detail axis is stored in map[string]any and read back with a .(string)
// assertion, so a task.TaskStatus has to be converted on the way in. Storing
// the typed value directly reads back as "" with no error anywhere — which is
// how demoteOrphanedItems briefly lost its "interrupted" marker.
func TestBackgroundStatusDetailRoundTripsThroughMetadata(t *testing.T) {
	for _, detail := range []task.TaskStatus{
		task.StatusFailed, task.StatusKilled, task.StatusStopped, StatusDetailInterrupted,
	} {
		item := &Item{ID: "1", Metadata: map[string]any{metaTaskID: "bg-1"}}
		setBackgroundStatusDetail(item, detail)

		if got := BackgroundStatusDetail(item); got != detail {
			t.Errorf("detail = %q, want %q", got, detail)
		}
		if !EndedAbnormally(item) {
			t.Errorf("%q should count as an abnormal end", detail)
		}
	}
}

// A task that finished normally is not an abnormal end, and neither is a plan
// item that never had a worker.
func TestEndedAbnormallyIgnoresNormalCompletionAndPlanItems(t *testing.T) {
	done := &Item{ID: "1", Metadata: map[string]any{metaTaskID: "bg-1"}}
	setBackgroundStatusDetail(done, task.StatusCompleted)
	if EndedAbnormally(done) {
		t.Error("a completed task should not count as an abnormal end")
	}

	if EndedAbnormally(&Item{ID: "2"}) {
		t.Error("a plan item has no worker and cannot have ended abnormally")
	}
}
