package conv

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestSubscriptionUsageStatusCluster(t *testing.T) {
	p := OperationModeParams{ModelName: "GPT", SubscriptionUsage: "ChatGPT week 80% left / 5h 90% left", Width: 160}
	wide := ansi.Strip(RenderModeStatus(p))
	if !strings.HasSuffix(wide, p.SubscriptionUsage) {
		t.Fatal(wide)
	}
	for _, width := range []int{12, 20, 40} {
		p.Width = width
		got := RenderModeStatus(p)
		if strings.Contains(ansi.Strip(got), "ChatGPT") || lipgloss.Width(got) > width {
			t.Fatalf("width %d: %q", width, got)
		}
	}
	p.SubscriptionUsage = ""
	p.Width = 160
	if strings.Contains(ansi.Strip(RenderModeStatus(p)), "ChatGPT") {
		t.Fatal("ungated label")
	}
}
