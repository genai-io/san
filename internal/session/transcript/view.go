package transcript

import (
	"time"

	"github.com/genai-io/san/internal/todo"
)

type MetadataView struct {
	ID              string
	Title           string
	LastPrompt      string
	Tag             string
	Mode            string
	AutoPilot       string
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
		AutoPilot:       t.State.AutoPilot,
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

func TrackerItemsFromView(items []TrackerItemView) []todo.Item {
	out := make([]todo.Item, 0, len(items))
	for _, item := range items {
		out = append(out, todo.Item{
			ID:          item.ID,
			Subject:     item.Subject,
			Description: item.Description,
			ActiveForm:  item.ActiveForm,
			// The view type carries the persisted wire string; the domain type
			// is where it becomes a checked lifecycle value.
			Status:          todo.Status(item.Status),
			Owner:           item.Owner,
			Blocks:          append([]string(nil), item.Blocks...),
			BlockedBy:       append([]string(nil), item.BlockedBy...),
			CreatedAt:       item.CreatedAt,
			UpdatedAt:       item.UpdatedAt,
			StatusChangedAt: item.StatusChangedAt,
		})
	}
	return out
}

func TrackerItemViewsFromItems(items []todo.Item) []TrackerItemView {
	out := make([]TrackerItemView, 0, len(items))
	for _, item := range items {
		out = append(out, TrackerItemView{
			ID:              item.ID,
			Subject:         item.Subject,
			Description:     item.Description,
			ActiveForm:      item.ActiveForm,
			Status:          string(item.Status),
			Owner:           item.Owner,
			Blocks:          append([]string(nil), item.Blocks...),
			BlockedBy:       append([]string(nil), item.BlockedBy...),
			CreatedAt:       item.CreatedAt,
			UpdatedAt:       item.UpdatedAt,
			StatusChangedAt: item.StatusChangedAt,
		})
	}
	return out
}
