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

// BackgroundTaskLaunch holds metadata for a newly spawned background task.
type BackgroundTaskLaunch struct {
	TaskID      string
	AgentName   string
	AgentType   string
	Description string
}

// TrackWorker creates or updates a tracker entry for a running background task.
func TrackWorker(svc Service, launch BackgroundTaskLaunch) {
	if existing := svc.FindByMetadata(metaTaskID, launch.TaskID); existing != nil {
		_ = svc.Update(existing.ID,
			WithSubject(workerSubject(launch)),
			WithDescription(launch.Description),
			WithStatus(StatusInProgress),
			WithMetadata(map[string]any{
				metaTaskID:       launch.TaskID,
				metaStatusDetail: string(task.StatusRunning),
			}),
		)
		return
	}

	entry := svc.Create(
		workerSubject(launch),
		launch.Description,
		"",
		map[string]any{
			metaTaskID:       launch.TaskID,
			metaStatusDetail: string(task.StatusRunning),
		},
	)
	opts := []UpdateOption{WithStatus(StatusInProgress)}
	if launch.AgentType != "" {
		opts = append(opts, WithOwner(launch.AgentType))
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
		subject = workerSubject(BackgroundTaskLaunch{
			TaskID:      info.ID,
			AgentName:   info.AgentName,
			AgentType:   info.AgentType,
			Description: info.Description,
		})
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

func workerSubject(launch BackgroundTaskLaunch) string {
	name := strings.TrimSpace(launch.AgentName)
	desc := strings.TrimSpace(launch.Description)
	switch {
	case name != "" && desc != "" && !strings.EqualFold(name, desc):
		return name + ": " + desc
	case desc != "":
		return desc
	case name != "":
		return name
	case launch.AgentType != "":
		return launch.AgentType
	default:
		return launch.TaskID
	}
}
