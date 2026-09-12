package session

import (
	"maps"

	"github.com/genai-io/san/internal/session/transcript"
	"github.com/genai-io/san/internal/todo"
)

// trackerItemViewsFromItems adapts the todo domain model at the session
// boundary; transcript deliberately owns only its persistence DTO. Export
// shares the store's metadata map, and the saved view is cached for the next
// state diff, so it must not see later in-place edits.
func trackerItemViewsFromItems(items []todo.Item) []transcript.TrackerItemView {
	out := make([]transcript.TrackerItemView, len(items))
	for i, item := range items {
		item.Metadata = maps.Clone(item.Metadata)
		out[i] = transcript.TrackerItemView(item)
	}
	return out
}

func trackerItemsFromViews(views []transcript.TrackerItemView) []todo.Item {
	out := make([]todo.Item, len(views))
	for i, view := range views {
		out[i] = todo.Item(view)
	}
	return out
}
