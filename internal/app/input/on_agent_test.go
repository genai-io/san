package input

import (
	"testing"

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
