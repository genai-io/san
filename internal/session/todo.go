package session

import (
	"maps"

	"github.com/genai-io/san/internal/todo"
	"github.com/genai-io/san/internal/transcript"
)

// todoSnapshots adapts the todo domain model at the session
// boundary; transcript deliberately owns only its persistence DTO. Export
// shares the store's metadata map, and the saved view is cached for the next
// state diff, so it must not see later in-place edits.
func todoSnapshots(items []todo.Item) []transcript.TodoItemSnapshot {
	out := make([]transcript.TodoItemSnapshot, len(items))
	for i, item := range items {
		item.Metadata = maps.Clone(item.Metadata)
		out[i] = transcript.TodoItemSnapshot(item)
	}
	return out
}

func todoItems(views []transcript.TodoItemSnapshot) []todo.Item {
	out := make([]todo.Item, len(views))
	for i, view := range views {
		out[i] = todo.Item(view)
	}
	return out
}
