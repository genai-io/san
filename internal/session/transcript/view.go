package transcript

import (
	"time"

	"github.com/genai-io/san/internal/task/tracker"
)

type MetadataView struct {
	ID              string
	Title           string
	LastPrompt      string
	Tag             string
	Mode            string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Provider        string
	Model           string
	Cwd             string
	MessageCount    int
	ParentSessionID string
}

func MetadataFromTranscript(t *Transcript) MetadataView {
	if t == nil {
		return MetadataView{}
	}
	return MetadataView{
		ID:              t.ID,
		Title:           t.State.Title,
		LastPrompt:      t.State.LastPrompt,
		Tag:             t.State.Tag,
		Mode:            t.State.Mode,
		CreatedAt:       t.CreatedAt,
		UpdatedAt:       t.UpdatedAt,
		Provider:        t.Provider,
		Model:           t.Model,
		Cwd:             t.Cwd,
		MessageCount:    len(t.Messages),
		ParentSessionID: t.ParentID,
	}
}

func MetadataFromListItem(item ListItem, cwd string) MetadataView {
	return MetadataView{
		ID:           item.SessionID,
		Title:        item.Title,
		LastPrompt:   item.LastPrompt,
		CreatedAt:    item.CreatedAt,
		UpdatedAt:    item.UpdatedAt,
		Cwd:          cwd,
		MessageCount: item.MessageCount,
	}
}

func TrackerTasksFromView(tasks []TrackerTaskView) []tracker.Task {
	out := make([]tracker.Task, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, tracker.Task{
			ID:              task.ID,
			Subject:         task.Subject,
			Description:     task.Description,
			ActiveForm:      task.ActiveForm,
			Status:          task.Status,
			Owner:           task.Owner,
			Blocks:          append([]string(nil), task.Blocks...),
			BlockedBy:       append([]string(nil), task.BlockedBy...),
			CreatedAt:       task.CreatedAt,
			UpdatedAt:       task.UpdatedAt,
			StatusChangedAt: task.StatusChangedAt,
		})
	}
	return out
}

func TrackerTaskViewsFromTasks(tasks []tracker.Task) []TrackerTaskView {
	out := make([]TrackerTaskView, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, TrackerTaskView{
			ID:              task.ID,
			Subject:         task.Subject,
			Description:     task.Description,
			ActiveForm:      task.ActiveForm,
			Status:          task.Status,
			Owner:           task.Owner,
			Blocks:          append([]string(nil), task.Blocks...),
			BlockedBy:       append([]string(nil), task.BlockedBy...),
			CreatedAt:       task.CreatedAt,
			UpdatedAt:       task.UpdatedAt,
			StatusChangedAt: task.StatusChangedAt,
		})
	}
	return out
}
