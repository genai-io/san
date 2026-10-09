package input

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/subagent"
)

func TestAgentSelectorShowsRuntimeModesWithoutTreatingThemAsDefinitions(t *testing.T) {
	registry := subagent.NewRegistry()
	selector := NewAgentSelector(registry)
	if err := selector.EnterSelect(120, 40); err != nil {
		t.Fatal(err)
	}
	if selector.list.activeTab != int(agentTabBuiltin) || len(selector.list.filtered) != 3 {
		t.Fatalf("built-in tab = %d, rows = %d; want three mode previews", selector.list.activeTab, len(selector.list.filtered))
	}
	for _, item := range selector.list.filtered {
		if !item.ModePreview || item.Source != "built-in" {
			t.Fatalf("mode row is toggleable: %+v", item)
		}
	}
	if cmd := selector.Toggle(); cmd != nil {
		t.Fatal("mode preview produced a toggle command")
	}
	if len(registry.ListConfigs()) != 0 {
		t.Fatal("mode previews were registered as named agents")
	}
}

func TestDefaultModePreviewUsesOneCompactRow(t *testing.T) {
	selector := NewAgentSelector(subagent.NewRegistry())
	if err := selector.EnterSelect(80, 24); err != nil {
		t.Fatal(err)
	}
	rendered := xansi.Strip(selector.Render())
	if strings.Count(rendered, "inherits session permissions and tools") != 1 {
		t.Fatalf("default mode inheritance is missing or repeated:\n%s", rendered)
	}
	if strings.Contains(rendered, "Tools:") || strings.Contains(rendered, "follows session mode") {
		t.Fatalf("default mode repeats its dynamic tool access:\n%s", rendered)
	}
}
