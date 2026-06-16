package input

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/genai-io/san/internal/app/kit"
)

// tabbedList is the shared substrate behind the category-tabbed overlay
// selectors (agent, skill): a kit.ListNav-backed item list, a row of
// count-bearing tabs, and a fuzzy search filter over the active tab. The two
// selectors were otherwise copy-pasting the tab/filter/keypress/frame
// mechanics; this holds them once, parameterised over the item type T plus a
// few pure hooks. The row layout and the Enter action — the parts that
// genuinely differ between selectors — stay in each selector.
//
// It lives flat in the input package rather than a separate selector package
// (see the reverted PR #214) and draws through kit's Panel/ListNav/tab helpers.
type tabbedList[T any] struct {
	// ── Configuration (set once at construction; never mutated) ──
	tabs        []tabSpec                  // left-to-right display + cycle order
	preferred   []int                      // initial-tab preference, indexes into tabs (empty ⇒ left-to-right)
	noun        string                     // item word for the empty-state messages
	placeholder string                     // search box prompt
	hints       []string                   // footer hint segments
	matchesTab  func(item T, tab int) bool // does item belong under tab?
	searchKeys  func(item T) []string      // strings the fuzzy filter matches against

	// ── State ───────────────────────────────────────────────────
	active    bool
	width     int
	height    int
	nav       kit.ListNav
	activeTab int
	items     []T // all loaded, before filtering
	filtered  []T // after tab + search filter
}

// tabSpec describes one category tab.
type tabSpec struct {
	name           string
	disableIfEmpty bool // grey the tab out when it currently holds no items
}

// load activates the list with a fresh item set and selects the first
// non-empty tab.
func (l *tabbedList[T]) load(items []T, width, height int) {
	l.items = items
	l.active = true
	l.width = width
	l.height = height
	l.nav.Reset()
	l.activeTab = l.firstNonEmptyTab()
	l.updateFilter()
}

// reset deactivates the list and drops its items.
func (l *tabbedList[T]) reset() {
	l.active = false
	l.items = nil
	l.filtered = nil
	l.nav.Reset()
	l.nav.Total = 0
}

func (l *tabbedList[T]) tabCount(tab int) int {
	count := 0
	for i := range l.items {
		if l.matchesTab(l.items[i], tab) {
			count++
		}
	}
	return count
}

func (l *tabbedList[T]) firstNonEmptyTab() int {
	if len(l.preferred) > 0 {
		for _, tab := range l.preferred {
			if l.tabCount(tab) > 0 {
				return tab
			}
		}
		return l.preferred[0]
	}
	for tab := range l.tabs {
		if l.tabCount(tab) > 0 {
			return tab
		}
	}
	return 0
}

func (l *tabbedList[T]) cycleTab(delta int) {
	n := len(l.tabs)
	if n == 0 {
		return
	}
	l.activeTab = ((l.activeTab+delta)%n + n) % n
	l.updateFilter()
}

// updateFilter rebuilds filtered from the active tab and the current search query.
func (l *tabbedList[T]) updateFilter() {
	query := strings.ToLower(l.nav.Search)
	l.filtered = l.filtered[:0]
	for i := range l.items {
		item := l.items[i]
		if !l.matchesTab(item, l.activeTab) {
			continue
		}
		if query != "" && !fuzzyMatchAny(l.searchKeys(item), query) {
			continue
		}
		l.filtered = append(l.filtered, item)
	}
	l.nav.ResetCursor()
	l.nav.Total = len(l.filtered)
}

// handleKey applies the shared key routing: Tab/←/→ cycle tabs, Enter runs the
// selector's action, ListNav owns navigation + search, Esc dismisses. onEnter
// is passed per call (a method value bound to the live selector) so the generic
// never has to hold a pointer back to it.
func (l *tabbedList[T]) handleKey(key tea.KeyMsg, onEnter func() tea.Cmd) tea.Cmd {
	switch key.String() {
	case "tab", "right":
		l.cycleTab(+1)
		return nil
	case "shift+tab", "left":
		l.cycleTab(-1)
		return nil
	case "enter":
		return onEnter()
	}

	searchChanged, consumed := l.nav.HandleKey(key)
	if searchChanged {
		l.updateFilter()
	}
	if consumed {
		return nil
	}

	if key.String() == "esc" {
		l.reset()
		return func() tea.Msg { return kit.DismissedMsg{} }
	}
	return nil
}

// render draws the panel frame (separator, tabs, search box, body, hints) and
// delegates the item rows to renderRows, which the caller supplies because the
// per-row layout is what differs between selectors. renderRows is only invoked
// when there is at least one filtered item; the empty state is handled here.
func (l *tabbedList[T]) render(renderRows func(body *strings.Builder, panel kit.Panel)) string {
	if !l.active {
		return ""
	}

	panel := kit.Panel{Width: l.width, Height: l.height}

	// Each item renders on 2 lines (row + spacer); the selected item adds one
	// sub-line. Reserve 2 lines for the more-above/more-below indicators.
	l.nav.MaxVisible = max(3, (panel.BodyHeight()-2)/2)
	l.nav.EnsureVisible()

	var sb strings.Builder

	sb.WriteString(panel.SeparatorLine())
	sb.WriteString("\n")
	sb.WriteString(l.renderTabs())
	sb.WriteString("\n\n")
	sb.WriteString(kit.RenderSearchBox(kit.SearchBoxOpts{
		Query:       l.nav.Search,
		Placeholder: l.placeholder,
		Filtered:    len(l.filtered),
		Total:       l.tabCount(l.activeTab),
		Width:       panel.ContentWidth(),
	}))
	sb.WriteString("\n\n")

	var body strings.Builder
	if len(l.filtered) == 0 {
		body.WriteString(l.renderEmpty())
	} else {
		renderRows(&body, panel)
	}
	sb.WriteString(panel.PadViewport(body.String()))

	sb.WriteString("\n")
	sb.WriteString(panel.SeparatorLine())
	sb.WriteString("\n")
	sb.WriteString(kit.HintLine(l.hints...))

	return panel.Wrap(sb.String())
}

func (l *tabbedList[T]) renderTabs() string {
	tabs := make([]kit.PanelTab, len(l.tabs))
	for i, spec := range l.tabs {
		count := l.tabCount(i)
		tabs[i] = kit.PanelTab{
			Name:    spec.name,
			Count:   count,
			Show:    true,
			Disable: spec.disableIfEmpty && count == 0,
		}
	}
	return kit.RenderPanelTabs(tabs, l.activeTab)
}

func (l *tabbedList[T]) renderEmpty() string {
	switch {
	case len(l.items) == 0:
		return kit.DimStyle().PaddingLeft(2).Render("No " + l.noun + " available")
	case l.tabCount(l.activeTab) == 0:
		return kit.DimStyle().PaddingLeft(2).Render(
			fmt.Sprintf("No %s %s — press Tab to switch tabs",
				strings.ToLower(l.tabs[l.activeTab].name), l.noun))
	default:
		return kit.DimStyle().PaddingLeft(2).Render("No " + l.noun + " match the filter")
	}
}

// fuzzyMatchAny reports whether query fuzzy-matches any of the candidate
// strings (each lowercased first).
func fuzzyMatchAny(candidates []string, query string) bool {
	for _, c := range candidates {
		if kit.FuzzyMatch(strings.ToLower(c), query) {
			return true
		}
	}
	return false
}
