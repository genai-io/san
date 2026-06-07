package kit

import (
	"strings"
	"testing"
)

// TestRenderPanelTabsCount confirms a tab's count is rendered only when it is
// greater than zero — so count-less tabs (e.g. /config) show just their name
// instead of a stray "0", while count-bearing tabs (skills/agents) keep it.
func TestRenderPanelTabsCount(t *testing.T) {
	tabs := []PanelTab{
		{Name: "withcount", Count: 7, Show: true},
		{Name: "nocount", Count: 0, Show: true},
	}
	out := RenderPanelTabs(tabs, 0)

	if !strings.Contains(out, "withcount 7") {
		t.Fatalf("a tab with a positive count should render it, got:\n%q", out)
	}
	if !strings.Contains(out, "nocount") {
		t.Fatalf("a count-less tab should still render its name, got:\n%q", out)
	}
	if strings.Contains(out, "0") {
		t.Fatalf("a zero count should not be rendered, got:\n%q", out)
	}
}
