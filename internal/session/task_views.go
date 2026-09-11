package session

import (
	"github.com/genai-io/san/internal/session/transcript"
	"github.com/genai-io/san/internal/todo"
)

// trackerItemViewsFromItems adapts the todo domain model at the session
// boundary. The transcript package deliberately owns only its persistence DTO.
func trackerItemViewsFromItems(items []todo.Item) []transcript.TrackerItemView {
	out := make([]transcript.TrackerItemView, 0, len(items))
	for _, item := range items {
		out = append(out, transcript.TrackerItemView{
			ID:              item.ID,
			Subject:         item.Subject,
			Description:     item.Description,
			ActiveForm:      item.ActiveForm,
			Status:          item.Status,
			Owner:           item.Owner,
			Metadata:        cloneItemMetadata(item.Metadata),
			Blocks:          append([]string(nil), item.Blocks...),
			BlockedBy:       append([]string(nil), item.BlockedBy...),
			CreatedAt:       item.CreatedAt,
			UpdatedAt:       item.UpdatedAt,
			StatusChangedAt: item.StatusChangedAt,
		})
	}
	return out
}

func trackerItemsFromViews(items []transcript.TrackerItemView) []todo.Item {
	out := make([]todo.Item, 0, len(items))
	for _, item := range items {
		out = append(out, todo.Item{
			ID:              item.ID,
			Subject:         item.Subject,
			Description:     item.Description,
			ActiveForm:      item.ActiveForm,
			Status:          item.Status,
			Owner:           item.Owner,
			Metadata:        cloneItemMetadata(item.Metadata),
			Blocks:          append([]string(nil), item.Blocks...),
			BlockedBy:       append([]string(nil), item.BlockedBy...),
			CreatedAt:       item.CreatedAt,
			UpdatedAt:       item.UpdatedAt,
			StatusChangedAt: item.StatusChangedAt,
		})
	}
	return out
}

func cloneItemMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return nil
	}
	out := make(map[string]any, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}
