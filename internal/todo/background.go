package todo

import (
	"strings"

	"github.com/genai-io/san/internal/task"
)

const (
	metaTaskID       = "background_task_id"
	metaStatusDetail = "background_status_detail"
)

// An Item is one of two kinds. A plan item is authored by the model and
// advanced by the main agent loop. A worker item mirrors a background task
// running in internal/task, joined to it by the metaTaskID metadata key. The
// helpers below are that join: they read what an item says about its
// background task, and resolve liveness against the live task manager rather
// than trusting the item's own recorded status.

// StatusDetailInterrupted marks a worker item whose background task died
// without reporting a terminal status — process exit, crash, or SIGKILL. Set
// by demoteOrphanedItems when a persisted store is adopted into a fresh
// session.
//
// It is a task.TaskStatus rather than a todo.Status: this axis records how the
// background task ended, not where the item sits in its own lifecycle. The
// value extends task's vocabulary with the one outcome only the tracker can
// observe — a task that stopped reporting rather than reporting a stop.
const StatusDetailInterrupted task.TaskStatus = "interrupted"

// BackgroundTaskID returns the background task ID this item mirrors, or ""
// when the item is a plan item authored by the model rather than a worker.
func BackgroundTaskID(item *Item) string {
	return metadataString(item, metaTaskID)
}

// BackgroundStatusDetail returns how a worker item's background task ended —
// task.StatusFailed, StatusKilled, StatusStopped, or StatusDetailInterrupted.
// Empty for items that mirror no background task, and for those that ended
// normally.
func BackgroundStatusDetail(item *Item) task.TaskStatus {
	return task.TaskStatus(metadataString(item, metaStatusDetail))
}

// WorkerRunning reports whether the background task behind this item is
// executing right now. False for items that name no background task, and for
// those the manager has no record of — a task it never knew or has forgotten
// cannot be running.
func WorkerRunning(item *Item) bool {
	id := BackgroundTaskID(item)
	return id != "" && task.Default().IsRunning(id)
}

// EndedAbnormally reports whether a worker item reached its terminal state by
// any route other than finishing its work.
func EndedAbnormally(item *Item) bool {
	switch BackgroundStatusDetail(item) {
	case task.StatusFailed, task.StatusKilled, task.StatusStopped, StatusDetailInterrupted:
		return true
	}
	return false
}

// setBackgroundStatusDetail records how a worker item's background task ended.
//
// Metadata is map[string]any and is read back with a .(string) assertion, so
// the value has to be stored as a plain string — putting a task.TaskStatus in
// the slot reads back as "" with no error anywhere. Routing every write through
// here keeps that conversion off the call sites.
func setBackgroundStatusDetail(item *Item, detail task.TaskStatus) {
	if item == nil {
		return
	}
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	item.Metadata[metaStatusDetail] = string(detail)
}

func metadataString(item *Item, key string) string {
	if item == nil || item.Metadata == nil {
		return ""
	}
	value, _ := item.Metadata[key].(string)
	return value
}

// TrackWorker creates or updates a tracker item for a running background task.
//
// It takes the same task.TaskInfo that CompleteWorker takes, because the two
// must be driven by the same source: the task manager's create and complete
// notifications. Feeding the two halves from different places lets them race,
// and a completion that arrives before its item exists is dropped for good —
// CompleteWorker has nothing to find, and the item created afterwards names a
// background task that already ended, so it sits in_progress for the whole session.
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

	item := svc.Create(workerSubject(info), info.Description, "", metadata)
	opts := []UpdateOption{WithStatus(StatusInProgress)}
	if info.AgentType != "" {
		opts = append(opts, WithOwner(info.AgentType))
	}
	_ = svc.Update(item.ID, opts...)
}

// CompleteWorker marks a tracker item as completed.
func CompleteWorker(svc Service, info task.TaskInfo) {
	item := svc.FindByMetadata(metaTaskID, info.ID)
	if item == nil {
		return
	}

	subject := item.Subject
	if subject == "" {
		subject = workerSubject(info)
	}

	statusDetail := info.Status
	if statusDetail == "" {
		statusDetail = task.StatusCompleted
	}

	_ = svc.Update(item.ID,
		WithSubject(subject),
		WithDescription(info.Description),
		WithStatus(StatusCompleted),
		WithMetadata(map[string]any{
			metaTaskID:       info.ID,
			metaStatusDetail: string(statusDetail),
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
