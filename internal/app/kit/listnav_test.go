package kit

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestListNav_MoveUpDown(t *testing.T) {
	n := ListNav{MaxVisible: 5, Total: 10}

	n.MoveDown()
	if n.Selected != 1 {
		t.Fatalf("expected Selected=1, got %d", n.Selected)
	}
	n.MoveUp()
	if n.Selected != 0 {
		t.Fatalf("expected Selected=0, got %d", n.Selected)
	}

	// Can't go above 0
	n.MoveUp()
	if n.Selected != 0 {
		t.Fatalf("expected Selected=0 (clamped), got %d", n.Selected)
	}

	// Can't go past Total-1
	n.Selected = 9
	n.MoveDown()
	if n.Selected != 9 {
		t.Fatalf("expected Selected=9 (clamped), got %d", n.Selected)
	}
}

func TestListNav_EnsureVisible(t *testing.T) {
	n := ListNav{MaxVisible: 3, Total: 10}

	// Move past visible window
	n.Selected = 4
	n.EnsureVisible()
	if n.Scroll != 2 {
		t.Fatalf("expected Scroll=2, got %d", n.Scroll)
	}

	// Move back above scroll
	n.Selected = 0
	n.EnsureVisible()
	if n.Scroll != 0 {
		t.Fatalf("expected Scroll=0, got %d", n.Scroll)
	}
}

func TestListNav_VisibleRange(t *testing.T) {
	n := ListNav{MaxVisible: 5, Total: 3}
	start, end := n.VisibleRange()
	if start != 0 || end != 3 {
		t.Fatalf("expected [0,3), got [%d,%d)", start, end)
	}

	n.Total = 10
	n.Scroll = 3
	start, end = n.VisibleRange()
	if start != 3 || end != 8 {
		t.Fatalf("expected [3,8), got [%d,%d)", start, end)
	}
}

func TestListNav_Reset(t *testing.T) {
	n := ListNav{Selected: 5, Scroll: 3, MaxVisible: 5, Total: 10, Search: "abc"}
	n.Reset()
	if n.Selected != 0 || n.Scroll != 0 || n.Search != "" {
		t.Fatalf("Reset should clear cursor and search: got sel=%d scroll=%d search=%q", n.Selected, n.Scroll, n.Search)
	}
	// MaxVisible and Total should be preserved
	if n.MaxVisible != 5 || n.Total != 10 {
		t.Fatalf("Reset should preserve MaxVisible and Total")
	}
}

func TestListNav_ResetCursor(t *testing.T) {
	n := ListNav{Selected: 5, Scroll: 3, Search: "abc"}
	n.ResetCursor()
	if n.Selected != 0 || n.Scroll != 0 {
		t.Fatalf("ResetCursor should clear cursor: got sel=%d scroll=%d", n.Selected, n.Scroll)
	}
	if n.Search != "abc" {
		t.Fatal("ResetCursor should preserve search")
	}
}

func TestListNav_HandleKey_Navigation(t *testing.T) {
	n := ListNav{MaxVisible: 5, Total: 10}

	// Arrow down
	changed, consumed := n.HandleKey(tea.KeyMsg{Type: tea.KeyDown})
	if changed || !consumed || n.Selected != 1 {
		t.Fatalf("Down: changed=%v consumed=%v sel=%d", changed, consumed, n.Selected)
	}

	// Arrow up
	changed, consumed = n.HandleKey(tea.KeyMsg{Type: tea.KeyUp})
	if changed || !consumed || n.Selected != 0 {
		t.Fatalf("Up: changed=%v consumed=%v sel=%d", changed, consumed, n.Selected)
	}

	// Ctrl+N
	changed, consumed = n.HandleKey(tea.KeyMsg{Type: tea.KeyCtrlN})
	if changed || !consumed || n.Selected != 1 {
		t.Fatalf("CtrlN: changed=%v consumed=%v sel=%d", changed, consumed, n.Selected)
	}
}

func TestListNav_HandleKey_VimKeysWhenSearchEmpty(t *testing.T) {
	n := ListNav{MaxVisible: 5, Total: 10}

	// j navigates when search is empty
	changed, consumed := n.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if changed || !consumed || n.Selected != 1 {
		t.Fatalf("j: changed=%v consumed=%v sel=%d", changed, consumed, n.Selected)
	}
	if n.Search != "" {
		t.Fatalf("j should not modify search, got %q", n.Search)
	}

	// k navigates when search is empty
	changed, consumed = n.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if changed || !consumed || n.Selected != 0 {
		t.Fatalf("k: changed=%v consumed=%v sel=%d", changed, consumed, n.Selected)
	}
}

func TestListNav_HandleKey_VimKeysAddToSearchWhenNonEmpty(t *testing.T) {
	n := ListNav{MaxVisible: 5, Total: 10, Search: "te"}

	// j adds to search when search is non-empty
	changed, consumed := n.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if !changed || !consumed {
		t.Fatalf("j with search: changed=%v consumed=%v", changed, consumed)
	}
	if n.Search != "tej" {
		t.Fatalf("expected search='tej', got %q", n.Search)
	}
}

func TestListNav_HandleKey_SearchRunes(t *testing.T) {
	n := ListNav{MaxVisible: 5, Total: 10}

	// Non j/k runes start search
	changed, consumed := n.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if !changed || !consumed || n.Search != "a" {
		t.Fatalf("a: changed=%v consumed=%v search=%q", changed, consumed, n.Search)
	}

	// Backspace removes from search
	changed, consumed = n.HandleKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if !changed || !consumed || n.Search != "" {
		t.Fatalf("backspace: changed=%v consumed=%v search=%q", changed, consumed, n.Search)
	}

	// Backspace on empty search is consumed but doesn't change search
	changed, consumed = n.HandleKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if changed || !consumed {
		t.Fatalf("backspace empty: changed=%v consumed=%v", changed, consumed)
	}
}

func TestListNav_HandleKey_BackspaceIsRuneSafe(t *testing.T) {
	n := ListNav{MaxVisible: 5, Total: 10, Search: "模型"}

	changed, consumed := n.HandleKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if !changed || !consumed {
		t.Fatalf("backspace: changed=%v consumed=%v", changed, consumed)
	}
	if n.Search != "模" {
		t.Fatalf("Search = %q, want %q", n.Search, "模")
	}
}

func TestListNav_HandleKey_EscClearsSearchFirst(t *testing.T) {
	n := ListNav{MaxVisible: 5, Total: 10, Search: "test"}

	// Esc clears search
	changed, consumed := n.HandleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !changed || !consumed || n.Search != "" {
		t.Fatalf("esc with search: changed=%v consumed=%v search=%q", changed, consumed, n.Search)
	}

	// Esc with empty search is not consumed (caller handles dismiss)
	changed, consumed = n.HandleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if changed || consumed {
		t.Fatalf("esc empty: changed=%v consumed=%v", changed, consumed)
	}
}
