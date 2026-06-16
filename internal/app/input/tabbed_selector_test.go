package input

import (
	"strings"
	"testing"

	"github.com/genai-io/san/internal/app/kit"
)

type tabTestItem struct {
	name string
	cat  int
	desc string
}

// newTestList builds a three-tab list (A, B, C) preferring C, then A, then B,
// matching items by their cat field and searching name+desc.
func newTestList() tabbedList[tabTestItem] {
	return tabbedList[tabTestItem]{
		tabs:        []tabSpec{{name: "A"}, {name: "B", disableIfEmpty: true}, {name: "C"}},
		preferred:   []int{2, 0, 1},
		noun:        "things",
		placeholder: "filter things...",
		hints:       []string{"Esc close"},
		matchesTab:  func(it tabTestItem, tab int) bool { return it.cat == tab },
		searchKeys:  func(it tabTestItem) []string { return []string{it.name, it.desc} },
		nav:         kit.ListNav{MaxVisible: 10},
	}
}

var testItems = []tabTestItem{
	{name: "alpha", cat: 0},
	{name: "beta", cat: 0},
	{name: "gamma", cat: 2},
}

func TestTabbedListLoadPicksPreferredNonEmptyTab(t *testing.T) {
	l := newTestList()
	l.load(testItems, 80, 24)

	// preferred is C(2), A(0), B(1); C has one item so it wins.
	if l.activeTab != 2 {
		t.Fatalf("activeTab = %d, want 2 (C)", l.activeTab)
	}
	if len(l.filtered) != 1 || l.filtered[0].name != "gamma" {
		t.Fatalf("filtered = %v, want [gamma]", l.filtered)
	}
	if got, want := l.tabCount(0), 2; got != want {
		t.Fatalf("tabCount(A) = %d, want %d", got, want)
	}
	if got, want := l.tabCount(1), 0; got != want {
		t.Fatalf("tabCount(B) = %d, want %d", got, want)
	}
}

func TestTabbedListCycleTabWraps(t *testing.T) {
	l := newTestList()
	l.load(testItems, 80, 24) // starts on C (2)

	l.cycleTab(+1) // 2 -> 0 (wrap)
	if l.activeTab != 0 {
		t.Fatalf("after +1 from C: activeTab = %d, want 0 (A)", l.activeTab)
	}
	if len(l.filtered) != 2 {
		t.Fatalf("on A: filtered = %d items, want 2", len(l.filtered))
	}

	l.cycleTab(-1) // 0 -> 2 (wrap back)
	if l.activeTab != 2 {
		t.Fatalf("after -1 from A: activeTab = %d, want 2 (C)", l.activeTab)
	}
}

func TestTabbedListSearchFiltersWithinTab(t *testing.T) {
	l := newTestList()
	l.load(testItems, 80, 24)
	l.cycleTab(+1) // move to A (alpha, beta)

	l.nav.Search = "alp"
	l.updateFilter()

	if len(l.filtered) != 1 || l.filtered[0].name != "alpha" {
		t.Fatalf("filtered = %v, want [alpha]", l.filtered)
	}
	if l.nav.Total != 1 {
		t.Fatalf("nav.Total = %d, want 1", l.nav.Total)
	}
}

func TestTabbedListRenderEmptyStates(t *testing.T) {
	cases := []struct {
		name  string
		setup func(l *tabbedList[tabTestItem])
		want  string
	}{
		{
			name:  "no items loaded",
			setup: func(l *tabbedList[tabTestItem]) { l.load(nil, 80, 24) },
			want:  "No things available",
		},
		{
			name: "active tab empty",
			setup: func(l *tabbedList[tabTestItem]) {
				l.load(testItems, 80, 24)
				l.activeTab = 1 // B has no items
				l.updateFilter()
			},
			want: "No b things — press Tab to switch tabs",
		},
		{
			name: "no search match",
			setup: func(l *tabbedList[tabTestItem]) {
				l.load(testItems, 80, 24)
				l.activeTab = 0
				l.nav.Search = "zzz"
				l.updateFilter()
			},
			want: "No things match the filter",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := newTestList()
			tc.setup(&l)
			if got := l.renderEmpty(); !strings.Contains(got, tc.want) {
				t.Fatalf("renderEmpty() = %q, want substring %q", got, tc.want)
			}
		})
	}
}

func TestTabbedListRenderDrawsRowsAndFrame(t *testing.T) {
	l := newTestList()
	l.load(testItems, 80, 24) // on C with one item

	out := l.render(func(body *strings.Builder, _ kit.Panel) {
		for _, it := range l.filtered {
			body.WriteString("ROW:" + it.name + "\n")
		}
	})

	if out == "" {
		t.Fatal("render() returned empty for an active list")
	}
	if !strings.Contains(out, "ROW:gamma") {
		t.Fatalf("render() missing delegated row, got:\n%s", out)
	}

	// Inactive lists render nothing.
	l.reset()
	if got := l.render(func(*strings.Builder, kit.Panel) {}); got != "" {
		t.Fatalf("render() after reset = %q, want empty", got)
	}
}
