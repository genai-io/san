package tracker

import (
	"testing"

	"github.com/genai-io/san/v2/internal/task"
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

	TrackWorker(Default(), BackgroundTaskLaunch{
		TaskID:      "bg-1",
		AgentName:   "dir-audit",
		AgentType:   "Explore",
		Description: "Directory structure audit",
	})

	tasks := Default().List()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 tracker task, got %d", len(tasks))
	}
	if tasks[0].Status != StatusInProgress {
		t.Fatalf("status = %q, want %q", tasks[0].Status, StatusInProgress)
	}
	if metadataStr(tasks[0].Metadata, metaTaskID) != "bg-1" {
		t.Fatalf("task ID metadata = %q", metadataStr(tasks[0].Metadata, metaTaskID))
	}
}

func TestCompleteWorkerUpdatesStatus(t *testing.T) {
	Initialize(Options{})
	t.Cleanup(func() { Default().Reset() })

	TrackWorker(Default(), BackgroundTaskLaunch{
		TaskID:      "bg-1",
		AgentName:   "dir-audit",
		AgentType:   "Explore",
		Description: "Directory audit",
	})

	CompleteWorker(Default(), task.TaskInfo{
		ID:     "bg-1",
		Type:   task.TaskTypeAgent,
		Status: task.StatusCompleted,
	})

	tasks := Default().List()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 tracker task, got %d", len(tasks))
	}
	if tasks[0].Status != StatusCompleted {
		t.Fatalf("status = %q, want %q", tasks[0].Status, StatusCompleted)
	}
	if metadataStr(tasks[0].Metadata, metaStatusDetail) != string(task.StatusCompleted) {
		t.Fatalf("status detail = %q", metadataStr(tasks[0].Metadata, metaStatusDetail))
	}
}

func TestCompleteWorkerTracksFailure(t *testing.T) {
	Initialize(Options{})
	t.Cleanup(func() { Default().Reset() })

	TrackWorker(Default(), BackgroundTaskLaunch{
		TaskID:      "bg-1",
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

	tasks := Default().List()
	if metadataStr(tasks[0].Metadata, metaStatusDetail) != string(task.StatusFailed) {
		t.Fatalf("status detail = %q, want %q", metadataStr(tasks[0].Metadata, metaStatusDetail), task.StatusFailed)
	}
}
