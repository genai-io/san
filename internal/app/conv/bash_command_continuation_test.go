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

func TestBashCommandContinuationExpandedKeepsDollarPrompt(t *testing.T) {
	for _, name := range []string{"Bash", "PowerShell"} {
		for _, command := range []string{"pwd", strings.Repeat("x", 320), "printf 'A     B'\n  echo 中文"} {
			input, err := json.Marshal(map[string]string{"command": command, "description": "Run\n tests"})
			if err != nil {
				t.Fatal(err)
			}
			for _, width := range []int{32, 40, 80, 100} {
				raw := RenderToolCalls(ToolCallsParams{
					ToolCalls: []core.ToolCall{{ID: "shell", Name: name, Input: string(input)}},
					Width:     width, ToolCallsExpanded: true,
				})
				got := ansi.Strip(raw)
				header, body, _ := strings.Cut(raw, "\n")
				if strings.Count(got, "$ ") != 1 || strings.Contains(got, "command:") || !strings.Contains(ansi.Strip(header), "Run tests") {
					t.Fatalf("%s width %d lost the shared shell presentation: %q", name, width, got)
				}
				if body != kit.RenderCommandBlock(command, width) {
					t.Fatalf("%s width %d changed command contents, indentation, or background: %q", name, width, got)
				}
			}
		}
	}
}
