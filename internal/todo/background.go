package todo

import (
	"strings"

	"github.com/genai-io/san/internal/task"
)

const (
	metaTaskID       = "background_task_id"
	metaStatusDetail = "background_status_detail"
)

// StatusDetailInterrupted marks a worker entry whose executor died without
// reporting a terminal status — process exit, crash, or SIGKILL. Set by
// demoteOrphanedTasks when a persisted store is adopted into a fresh session.
const StatusDetailInterrupted = "interrupted"

// BackgroundTaskID returns the background task ID backing this entry, or ""
// when the entry is a plan item authored by the model rather than a worker.
// Callers resolve liveness by looking the ID up in the live task manager.
func BackgroundTaskID(t *Task) string {
	return metadataString(t, metaTaskID)
}

// BackgroundStatusDetail returns how a worker entry's executor ended —
// "failed", "killed", "stopped", or StatusDetailInterrupted. Empty for entries
// that are not backed by a worker, and for workers that ended normally.
func BackgroundStatusDetail(t *Task) string {
	return metadataString(t, metaStatusDetail)
}

// WorkerRunning reports whether the background task backing this entry is
// executing right now. False for entries that name no worker, and for workers
// the manager has no record of — a task it never knew or has forgotten cannot
// be running.
//
// This is the read side of the join TrackWorker writes, and lives here so the
// metadata key and the entry↔executor correspondence stay in one package.
func WorkerRunning(t *Task) bool {
	id := BackgroundTaskID(t)
	return id != "" && task.Default().IsRunning(id)
}

// EndedAbnormally reports whether a worker entry reached its terminal state by
// any route other than finishing its work. The stored detail is written as
// string(task.TaskStatus) by CompleteWorker, so it is matched against those
// constants rather than loose literals.
func EndedAbnormally(t *Task) bool {
	switch BackgroundStatusDetail(t) {
	case string(task.StatusFailed), string(task.StatusKilled),
		string(task.StatusStopped), StatusDetailInterrupted:
		return true
	}
	return false
}

func metadataString(t *Task, key string) string {
	if t == nil || t.Metadata == nil {
		return ""
	}
	value, _ := t.Metadata[key].(string)
	return value
}

// TrackWorker creates or updates a tracker entry for a running background task.
//
// It takes the same task.TaskInfo that CompleteWorker takes, because the two
// must be driven by the same source: the task manager's create and complete
// notifications. Feeding the two halves from different places lets them race,
// and a completion that arrives before its entry exists is dropped for good —
// CompleteWorker has nothing to find, and the entry created afterwards names a
// task that already ended, so it sits in_progress for the rest of the session.
func TrackWorker(svc Service, info task.TaskInfo) {
	if info.ID == "" {
		return
	}
	metadata := map[string]any{
		metaTaskID:       info.ID,
		metaStatusDetail: string(task.StatusRunning),
	}

	if existing := svc.FindByMetadata(metaTaskID, info.ID); existing != nil {
		_ = svc.Update(existing.ID,
			WithSubject(workerSubject(info)),
			WithDescription(info.Description),
			WithStatus(StatusInProgress),
			WithMetadata(metadata),
		)
		return
	}

	entry := svc.Create(workerSubject(info), info.Description, "", metadata)
	opts := []UpdateOption{WithStatus(StatusInProgress)}
	if info.AgentType != "" {
		opts = append(opts, WithOwner(info.AgentType))
	}
	_ = svc.Update(entry.ID, opts...)
}

// CompleteWorker marks a tracker entry as completed.
func CompleteWorker(svc Service, info task.TaskInfo) {
	entry := svc.FindByMetadata(metaTaskID, info.ID)
	if entry == nil {
		return
	}

	subject := entry.Subject
	if subject == "" {
		subject = workerSubject(info)
	}

	statusDetail := string(info.Status)
	if statusDetail == "" {
		statusDetail = string(task.StatusCompleted)
	}

	_ = svc.Update(entry.ID,
		WithSubject(subject),
		WithDescription(info.Description),
		WithStatus(StatusCompleted),
		WithMetadata(map[string]any{
			metaTaskID:       info.ID,
			metaStatusDetail: statusDetail,
		}),
	)
}

func workerSubject(info task.TaskInfo) string {
	name := strings.TrimSpace(info.AgentName)
	desc := strings.TrimSpace(info.Description)
	switch {
	case name != "" && desc != "" && !strings.EqualFold(name, desc):
		return name + ": " + desc
	case desc != "":
		return desc
	case name != "":
		return name
	case info.AgentType != "":
		return info.AgentType
	// A bash worker names no agent, so its command is the only description of
	// itself it carries. Better than falling through to the opaque task ID.
	case info.Command != "":
		return info.Command
	default:
		return info.ID
	}
}
