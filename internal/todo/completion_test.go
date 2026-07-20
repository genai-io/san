package todo

import "testing"

// AllMarkedCompleted gates two things: whether the tracker panel is drawn, and
// whether the turn-end reset may discard the list. Both want the same question
// answered — has the model closed out everything it wrote down — so the cases
// that must not read as complete are the ones where work was recorded and left
// open, however it was left open.
func TestAllMarkedCompleted(t *testing.T) {
	cases := []struct {
		name     string
		statuses []string
		want     bool
	}{
		{"empty store", nil, false},
		{"all completed", []string{StatusCompleted, StatusCompleted}, true},
		{"one still pending", []string{StatusCompleted, StatusPending}, false},
		// The case the tracker used to get wrong elsewhere: an item the model
		// marked in progress and never closed. No executor is advancing it, but
		// it is abandoned work rather than finished work, so the list is not
		// complete and the list stays up showing it as [stalled].
		{"one left in progress", []string{StatusCompleted, StatusInProgress}, false},
		{"deleted items do not count as open", []string{StatusCompleted, StatusDeleted}, true},
		// Tombstones only: the map is not empty, so the emptiness check passes
		// and the loop skips every item. The view never sees this — List drops
		// deleted items, so it renders nothing either way.
		{"only tombstones", []string{StatusDeleted}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewStore()
			for _, status := range tc.statuses {
				item := store.Create("item", "", "", nil)
				var err error
				if status == StatusDeleted {
					err = store.Delete(item.ID)
				} else {
					err = store.Update(item.ID, WithStatus(status))
				}
				if err != nil {
					t.Fatalf("set item %s to %s: %v", item.ID, status, err)
				}
			}
			if got := store.AllMarkedCompleted(); got != tc.want {
				t.Fatalf("AllMarkedCompleted() = %v, want %v", got, tc.want)
			}
		})
	}
}
