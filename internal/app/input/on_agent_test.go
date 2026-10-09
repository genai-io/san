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

func TestModePreviewShowsToolsOnSecondLine(t *testing.T) {
	for _, tc := range []struct {
		name  string
		index int
	}{
		{name: "Explorer", index: 1},
		{name: "Editor", index: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selector := NewAgentSelector(subagent.NewRegistry())
			if err := selector.EnterSelect(120, 30); err != nil {
				t.Fatal(err)
			}
			selector.list.filtered[tc.index].Tools = "Read, Bash"
			selector.list.nav.Selected = tc.index
			lines := strings.Split(xansi.Strip(selector.Render()), "\n")
			for i, line := range lines {
				if !strings.Contains(line, tc.name) || !strings.Contains(line, "◇") {
					continue
				}
				if i+2 >= len(lines) || !strings.Contains(lines[i+1], "Tools: Read, Bash") || strings.TrimSpace(lines[i+2]) != "" {
					t.Fatalf("mode preview is not two lines:\n%s", strings.Join(lines, "\n"))
				}
				return
			}
			t.Fatalf("mode row not rendered:\n%s", strings.Join(lines, "\n"))
		})
	}
}
