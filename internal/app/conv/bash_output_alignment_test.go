package conv

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/kit"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/san/internal/tool/toolresult"
)

func TestBashResultGuidesShareCommandColumnAndStayQuiet(t *testing.T) {
	wasDark := kit.IsDarkBackground()
	t.Cleanup(func() {
		if wasDark {
			kit.InitTheme("dark")
		} else {
			kit.InitTheme("light")
		}
	})
	for _, theme := range []string{"light", "dark"} {
		kit.InitTheme(theme)
		for _, width := range []int{10, 12, 32, 40, 100} {
			command := ansi.Strip(renderShellToolCall("Bash", `{"command":"echo first\necho last"}`, width, "●", ""))
			header, body, _ := strings.Cut(command, "\n")
			column := strings.Index(body, "$")
			if column != lipgloss.Width(header[:strings.Index(header, "Bash")]) {
				t.Fatalf("%s width %d: shell markers must align with the tool label: %q", theme, width, command)
			}
			details := toolresult.BashDetails{LineCount: 30, Tail: "one\ntwo\n"}
			raw := renderBashToolResultInline(ToolResultData{ToolName: "Bash", Content: "one\ntwo\n", Details: details, BashPreviewLines: 2, Width: width})
			rows := strings.Split(strings.TrimSuffix(ansi.Strip(raw), "\n"), "\n")
			omission := "… (28)"
			if width == 10 {
				omission = "│ …"
			}
			if len(rows) != 3 || !strings.HasSuffix(rows[0], omission) || !strings.HasSuffix(rows[2], "└ two") {
				t.Fatalf("%s width %d: missing counted omission or closure: %q", theme, width, rows)
			}
			for _, row := range rows {
				marker := strings.TrimLeft(row, " ")
				if len(row)-len(marker) != column || ansi.StringWidth(row) >= width {
					t.Fatalf("%s width %d: command/result columns differ: %q", theme, width, row)
				}
			}
			guide := lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextDisabled).Render("│")
			if !strings.Contains(raw, guide+" ") || !strings.Contains(raw, toolResultStyle.Render("one")) {
				t.Fatalf("%s: guide and body lost their separate colors: %q", theme, raw)
			}
			if kit.CurrentTheme.TextDisabled == kit.CurrentTheme.TextDim {
				t.Fatalf("%s: guide must be quieter than output", theme)
			}
		}
	}
}

func TestBashByteCappedLiveTailKeepsExactOmission(t *testing.T) {
	output := strings.Repeat(strings.Repeat("x", 3000)+"\n", 100)
	tail := output[len(output)-4096:]
	call := core.ToolCall{ID: "large-output", Name: "Bash", Input: `{"command":"build"}`}
	view := ansi.Strip(RenderToolCalls(ToolCallsParams{
		ToolCalls: []core.ToolCall{call}, Width: 40, BashPreviewLines: 2,
		ToolProgress: map[string]ToolProgress{call.ID: {Counter: "100 lines", Tail: tail, LineCount: 100, TailTruncated: true}},
	}))
	if !strings.Contains(view, "  │ … (98)\n") || strings.Contains(view, "└") {
		t.Fatalf("live byte-capped output lost exact omission or closed early:\n%s", view)
	}
	partial := renderOutputPreview(strings.Repeat("x", 4096), 2, 40, toolresult.BashDetails{LineCount: 1, TailTruncated: true}, false)
	if plain := ansi.Strip(partial); !strings.HasPrefix(plain, "  │ …\n") || strings.Contains(plain, "… (") {
		t.Fatalf("a clipped partial row invented omitted rows: %q", plain)
	}
	legacy := ansi.Strip(renderOutputPreview("one\ntwo\nthree\n", 2, 40, toolresult.BashDetails{}, false))
	if !strings.HasPrefix(legacy, "  │ …\n") || strings.Contains(legacy, "… (") {
		t.Fatalf("legacy progress guessed a total count: %q", legacy)
	}
}

func TestBashExpandedOutputClosesLastPhysicalRowWithoutCountFooter(t *testing.T) {
	output := strings.Repeat("long ", 30) + "\n\n"
	data := ToolResultData{ToolName: "Bash", Content: output, Width: 32, Expanded: true,
		Details: toolresult.BashDetails{LineCount: 2}}
	plain := ansi.Strip(renderBashToolResultInline(data))
	rows := strings.Split(strings.TrimSuffix(plain, "\n"), "\n")
	var recovered strings.Builder
	for i, row := range rows {
		prefix := "  │ "
		if i == len(rows)-1 {
			prefix = "  └ "
		}
		if !strings.HasPrefix(row, prefix) || ansi.StringWidth(row) >= 32 {
			t.Fatalf("expanded row lost its guide or overflowed: %q", row)
		}
		recovered.WriteString(strings.TrimPrefix(row, prefix))
	}
	if recovered.String() != strings.TrimSuffix(strings.TrimSuffix(output, "\n"), "\n") || strings.Contains(plain, "2 lines") || strings.Count(plain, "└") != 1 {
		t.Fatalf("expanded output was dropped or gained a count footer: %q", plain)
	}
	data.IsError = true
	data.Details = toolresult.BashDetails{LineCount: 2, Error: "exit code 7", Tail: output}
	failed := ansi.Strip(renderBashToolResultInline(data))
	if !strings.HasSuffix(failed, "  │ \n  └ failed · exit code 7\n") || strings.Count(failed, "└") != 1 {
		t.Fatalf("failure lost trailing blank output or truthful closure: %q", failed)
	}
}
