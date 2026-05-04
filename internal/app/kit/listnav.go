package kit

import tea "github.com/charmbracelet/bubbletea"

// ListNav manages cursor position, scroll offset, and search state for
// list-based selectors. Embed it in selector Models to eliminate the
// duplicated MoveUp/MoveDown/ensureVisible/HandleKey boilerplate.
//
// The caller must set Total after every filter/data change so that
// navigation bounds are correct.
type ListNav struct {
	Selected   int
	Scroll     int
	MaxVisible int
	Total      int    // set by caller after filtering
	Search     string // current search/filter query
}

// MoveUp moves the cursor up one position.
func (n *ListNav) MoveUp() {
	if n.Selected > 0 {
		n.Selected--
		n.EnsureVisible()
	}
}

// MoveDown moves the cursor down one position.
func (n *ListNav) MoveDown() {
	if n.Selected < n.Total-1 {
		n.Selected++
		n.EnsureVisible()
	}
}

// EnsureVisible adjusts Scroll so that Selected stays within the visible window.
func (n *ListNav) EnsureVisible() {
	if n.Selected < n.Scroll {
		n.Scroll = n.Selected
	}
	if n.Selected >= n.Scroll+n.MaxVisible {
		n.Scroll = n.Selected - n.MaxVisible + 1
	}
}

// ResetCursor resets cursor and scroll to the top without clearing search.
func (n *ListNav) ResetCursor() {
	n.Selected = 0
	n.Scroll = 0
}

// Reset resets cursor, scroll, and search state.
func (n *ListNav) Reset() {
	n.Selected = 0
	n.Scroll = 0
	n.Search = ""
}

// VisibleRange returns the start (inclusive) and end (exclusive) indices
// for the currently visible window of items.
func (n *ListNav) VisibleRange() (start, end int) {
	end = n.Scroll + n.MaxVisible
	if end > n.Total {
		end = n.Total
	}
	return n.Scroll, end
}

// HandleKey processes navigation and search keys common to all list selectors.
//
// Returns:
//   - searchChanged: true if the search query was modified (caller should re-filter)
//   - consumed: true if the key was handled
//
// Esc with an empty search returns consumed=false so callers can handle dismiss.
// Enter and other action keys are NOT handled — callers implement those.
func (n *ListNav) HandleKey(key tea.KeyMsg) (searchChanged, consumed bool) {
	switch key.Type {
	case tea.KeyUp, tea.KeyCtrlP:
		n.MoveUp()
		return false, true
	case tea.KeyDown, tea.KeyCtrlN:
		n.MoveDown()
		return false, true
	case tea.KeyEsc:
		if n.Search != "" {
			n.Search = ""
			return true, true
		}
		return false, false
	case tea.KeyBackspace:
		if len(n.Search) > 0 {
			runes := []rune(n.Search)
			n.Search = string(runes[:len(runes)-1])
			return true, true
		}
		return false, true
	case tea.KeyRunes:
		// Vim-style navigation takes priority when search is empty.
		if n.Search == "" {
			switch key.String() {
			case "j":
				n.MoveDown()
				return false, true
			case "k":
				n.MoveUp()
				return false, true
			}
		}
		n.Search += string(key.Runes)
		return true, true
	}
	return false, false
}
