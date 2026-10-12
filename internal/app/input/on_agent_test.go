package input

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/subagent"
	_ "github.com/genai-io/san/internal/tool/register"
)

func TestAgentSelectorShowsOnlyOneDefaultAgent(t *testing.T) {
	registry := subagent.NewRegistry()
	selector := NewAgentSelector(registry)
	if err := selector.EnterSelect(120, 40); err != nil {
		t.Fatal(err)
	}
	if selector.list.activeTab != int(agentTabBuiltin) || len(selector.list.filtered) != 1 || selector.list.filtered[0].Name != "Agent" {
		t.Fatalf("built-in tab = %d, rows = %+v; want only Agent", selector.list.activeTab, selector.list.filtered)
	}
	for _, item := range selector.list.filtered {
		if !item.BuiltinPreview || item.Source != "built-in" {
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

func TestAgentSelectorToggleWithBuiltinPreviewName(t *testing.T) {
	for _, tc := range []struct {
		source string
		tab    agentTab
	}{
		{source: "user", tab: agentTabUser},
		{source: "project", tab: agentTabProject},
	} {
		for _, name := range []string{"Agent", "Default", "Explorer", "Editor"} {
			t.Run(tc.source+"/"+name, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("USERPROFILE", home)
				cwd := t.TempDir()
				registry := subagent.NewRegistry()
				registry.Register(&subagent.AgentConfig{Name: name, Source: tc.source})
				if err := registry.InitStores(cwd); err != nil {
					t.Fatal(err)
				}
				selector := NewAgentSelector(registry)
				if err := selector.EnterSelect(120, 40); err != nil {
					t.Fatal(err)
				}
				if selector.list.activeTab != int(tc.tab) || len(selector.list.filtered) != 1 {
					t.Fatalf("configured tab = %d, rows = %d", selector.list.activeTab, len(selector.list.filtered))
				}

				for _, enabled := range []bool{false, true} {
					cmd := selector.Toggle()
					if cmd == nil {
						t.Fatal("configured agent did not produce a toggle command")
					}
					if msg, ok := cmd().(AgentToggleMsg); !ok || msg.AgentName != name || msg.Enabled != enabled {
						t.Fatalf("toggle message = %+v; want %s enabled=%v", msg, name, enabled)
					}

					selector.list.cycleTab(-int(tc.tab))
					for _, preview := range selector.list.filtered {
						if !preview.BuiltinPreview || !preview.Enabled {
							t.Fatalf("toggle changed mode preview: %+v", preview)
						}
					}
					selector.list.cycleTab(int(tc.tab))
					if got := selector.list.filtered[0].Enabled; got != enabled {
						t.Fatalf("enabled after switching tabs = %v; want %v", got, enabled)
					}

					selector.list.nav.Search = name[:3]
					selector.list.updateFilter()
					if len(selector.list.filtered) != 1 || selector.list.filtered[0].Enabled != enabled {
						t.Fatalf("search restored stale toggle state: %+v", selector.list.filtered)
					}
					selector.list.nav.Search = ""
					selector.list.updateFilter()

					reloaded := subagent.NewRegistry()
					if err := reloaded.InitStores(cwd); err != nil {
						t.Fatal(err)
					}
					if got := reloaded.IsEnabled(name); got != enabled {
						t.Fatalf("persisted enabled = %v; want %v", got, enabled)
					}
				}
			})
		}
	}
}

func TestDefaultBuiltinPreviewUsesOneCompactRow(t *testing.T) {
	selector := NewAgentSelector(subagent.NewRegistry())
	if err := selector.EnterSelect(80, 24); err != nil {
		t.Fatal(err)
	}
	rendered := xansi.Strip(selector.Render())
	if strings.Count(rendered, "inherits session permissions") != 1 {
		t.Fatalf("default mode inheritance is missing or repeated:\n%s", rendered)
	}
	if strings.Contains(rendered, "Tools:") || strings.Contains(rendered, "follows session mode") {
		t.Fatalf("default mode repeats its dynamic tool access:\n%s", rendered)
	}
}

func TestAgentBuiltinPreviewDoesNotAdvertiseVariants(t *testing.T) {
	for _, width := range []int{80, 120} {
		selector := NewAgentSelector(subagent.NewRegistry())
		if err := selector.EnterSelect(width, 30); err != nil {
			t.Fatal(err)
		}
		view := xansi.Strip(selector.Render())
		for _, removed := range []string{"Explorer", "Editor", "General", "explore ·", "edit ·", "Tools:"} {
			if strings.Contains(view, removed) {
				t.Fatalf("default Agent advertises %q:\n%s", removed, view)
			}
		}
	}
}
