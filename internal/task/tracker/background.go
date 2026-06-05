package tracker

import (
	"strings"

	"github.com/genai-io/san/v2/internal/task"
)

const (
	metaTaskID       = "background_task_id"
	metaStatusDetail = "background_status_detail"
)

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
