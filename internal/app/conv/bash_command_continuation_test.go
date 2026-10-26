package conv

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestBashCommandContinuationProduction(t *testing.T) {
	oldProfile := lipgloss.Writer.Profile
	lipgloss.Writer.Profile = colorprofile.TrueColor
	t.Cleanup(func() { lipgloss.Writer.Profile = oldProfile })
	commands := []string{
		"printf '%s' " + strings.Repeat("argument ", 24) + "end",
		"printf '" + strings.Repeat("中文", 60) + "'",
		"printf 'A     B'\t" + strings.Repeat("quoted-space ", 20),
		"echo first\n\n  printf 'A     B' \\\n\t" + strings.Repeat("next ", 30) + "\n",
	}
	for _, width := range []int{32, 40, 80, 100} {
		for i, command := range commands {
			t.Run(fmt.Sprintf("width=%d/command=%d", width, i), func(t *testing.T) {
				input, err := json.Marshal(map[string]string{"command": command})
				if err != nil {
					t.Fatal(err)
				}
				call := core.ToolCall{ID: "shell", Name: "Bash", Input: string(input)}
				params := ToolCallsParams{ToolCalls: []core.ToolCall{call}, Width: width, BashPreviewLines: 4,
					ResultMap: map[string]ToolResultData{call.ID: {ToolName: "Bash", Content: "first\nlast", Details: toolresult.BashDetails{LineCount: 2}}}}
				raw := RenderToolCalls(params)
				_, body, ok := strings.Cut(raw, "\n")
				block := kit.RenderCommandBlock(command, width)
				if !ok || !strings.HasPrefix(body, block) {
					t.Fatalf("production rewrapped or changed command block: %q", raw)
				}
				rows := strings.Split(strings.TrimSuffix(block, "\n"), "\n")
				style := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Text).Background(kit.CurrentTheme.CommandBg)
				for j, row := range rows {
					plain := ansi.Strip(row)
					prefix := "    "
					if j == 0 {
						prefix = "  $ "
					}
					if !strings.HasPrefix(plain, prefix) || ansi.StringWidth(plain) != width-1 || row != "  "+style.Render(plain[2:]) {
						t.Fatalf("row %d lost hanging gutter, margin, or rectangle: %q", j, row)
					}
				}
				if !strings.HasSuffix(ansi.Strip(raw), "  │ first\n  └ last\n") {
					t.Fatalf("output rails no longer share prompt column 2: %q", raw)
				}
			})
		}
	}
}

func TestBashCommandContinuationExpandedKeepsIndent(t *testing.T) {
	command := strings.Repeat("x", 320)
	input, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{32, 40, 80, 100} {
		got := ansi.Strip(RenderToolCalls(ToolCallsParams{
			ToolCalls: []core.ToolCall{{ID: "shell", Name: "Bash", Input: string(input)}},
			Width:     width, ToolCallsExpanded: true,
		}))
		_, body, _ := strings.Cut(got, "\n")
		rows := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
		if rows[0] != "    command:" {
			t.Fatalf("expanded command label changed: %q", got)
		}
		var restored strings.Builder
		for _, row := range rows[1:] {
			if !strings.HasPrefix(row, "    ") || ansi.StringWidth(row) > width {
				t.Fatalf("width %d lost continuation alignment: %q", width, row)
			}
			restored.WriteString(strings.TrimPrefix(row, "    "))
		}
		if restored.String() != command {
			t.Fatalf("width %d changed expanded command content: %q", width, got)
		}
	}
}
