package conv

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestFileChangeSummaryProduction(t *testing.T) {
	for _, tc := range []struct {
		name    string
		details toolresult.FileChangeDetails
		want    string
		added   string
		removed string
	}{
		{"new plural", toolresult.FileChangeDetails{IsNewFile: true, AddedLines: 29}, "new file · +29 lines", "+29 lines", ""},
		{"new singular", toolresult.FileChangeDetails{IsNewFile: true, AddedLines: 1}, "new file · +1 line", "+1 line", ""},
		{"new empty", toolresult.FileChangeDetails{IsNewFile: true}, "new file · 0 lines", "", ""},
		{"both", toolresult.FileChangeDetails{AddedLines: 3, RemovedLines: 1}, "+3 -1", "+3", "-1"},
		{"add only", toolresult.FileChangeDetails{AddedLines: 3}, "+3", "+3", ""},
		{"remove only", toolresult.FileChangeDetails{RemovedLines: 2}, "-2", "", "-2"},
		{"unchanged", toolresult.FileChangeDetails{}, "no changes", "", ""},
	} {
		for _, nested := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/nested=%v", tc.name, nested), func(t *testing.T) {
				name := "Edit"
				if tc.details.IsNewFile {
					name = "Write"
				}
				raw := RenderToolResultInline(ToolResultData{ToolName: name, Details: tc.details, Nested: nested, Width: 80}, nil)
				plain := ansi.Strip(raw)
				prefix := name + " → "
				if nested {
					prefix = "  └ "
				}
				if !strings.Contains(plain, prefix+tc.want+"\n") || strings.Contains(plain, "+0") || strings.Contains(plain, "-0") {
					t.Fatalf("unexpected change summary: %q", plain)
				}
				for _, color := range []struct {
					text  string
					style lipgloss.Style
				}{
					{tc.added, lipgloss.NewStyle().Foreground(kit.CurrentTheme.Success)},
					{tc.removed, lipgloss.NewStyle().Foreground(kit.CurrentTheme.Error)},
				} {
					if color.text == "" {
						continue
					}
					colored := color.style.Render(color.text)
					if colored == color.text || !strings.Contains(raw, colored) {
						t.Fatalf("outer summary styling lost semantic count color for %q: %q", color.text, raw)
					}
				}
			})
		}
	}
}
