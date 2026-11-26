package transcript

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/genai-io/san/internal/app/tui/widgets"
	"github.com/genai-io/san/internal/core"
	"github.com/genai-io/sdk-go/pkg/ai"
)

func TestReadSourceInTranscript(t *testing.T) {
	const content = "     1\tleft\rright\n     2\t\nMore lines available."
	result := core.ChatMessage{Role: core.ChatUser, Expanded: true, ToolResult: &core.ToolResult{ToolCallID: "read", ToolName: "Read", Content: ai.TextContent(content)}}
	for _, paired := range []bool{false, true} {
		msgs := []core.ChatMessage{result}
		if paired {
			msgs = append([]core.ChatMessage{{Role: core.ChatAssistant, ToolCalls: []core.ToolCall{{ID: "read", Name: "Read", Input: `{"file_path":"sample.txt"}`}}}}, msgs...)
		}
		p := RenderContext{Messages: msgs, Width: 32, InlinedResults: PrecomputeInlinedResults(msgs, 0)}
		got := xansi.Strip(RenderMessageRange(p, 0, len(msgs), false))
		if !strings.Contains(got, "2 lines") || !strings.Contains(got, "left␍right") || !strings.Contains(got, "     2") || !strings.Contains(got, "More lines available.") || strings.Contains(got, "\r") {
			t.Errorf("paired %t: %q", paired, got)
		}
	}
}

func TestFailedEditTabPreview(t *testing.T) {
	profile, dark := lipgloss.Writer.Profile, widgets.IsDarkBackground()
	lipgloss.Writer.Profile = colorprofile.TrueColor
	t.Cleanup(func() {
		lipgloss.Writer.Profile = profile
		if dark {
			widgets.InitTheme("dark")
		} else {
			widgets.InitTheme("light")
		}
	})
	data := ToolResultData{IsError: true, Width: 12, Content: "Error: miss", ToolInput: `{"old_string":"\tabcdef","new_string":"\tuvwxyz"}`}
	for _, theme := range []string{"dark", "light"} {
		widgets.InitTheme(theme)
		got := renderNestedFileChangeResultInline(data)
		want := ""
		for _, row := range []string{"  -     abcd", "  - ef", "  +     uvwx", "  + yz", "  ┊ miss"} {
			want += toolResultStyle.Render(row) + "\n"
		}
		want += errorStyle.Render("  └ failed") + "\n"
		if got != want {
			t.Errorf("%s: got %q, want %q", theme, xansi.Strip(got), xansi.Strip(want))
		}
	}
}

func TestNestedGenericSizeIncludesTerminator(t *testing.T) {
	for _, tc := range []struct{ tool, content, want string }{{"WebFetch", "é\n", "3 B"}, {"Grep", "match\n\n", "2 lines"}} {
		data := ToolResultData{ToolName: tc.tool, Content: tc.content, Width: 12}
		got := renderNestedGenericToolResultInline(data)
		if want := toolResultStyle.Render("  └ "+tc.want) + "\n"; got != want {
			t.Errorf("%s: got %q, want %q", tc.tool, got, want)
		}
	}
}

func TestStandalonePlainEditSummary(t *testing.T) {
	data := ToolResultData{ToolName: "Edit", Content: "Edited src/copy(old).go (1 replacement(s), +1 -1)"}
	got := xansi.Strip(renderFileChangeResultInline(data))
	want := "  " + toolResultIcon(false) + "  Edit → 1 replacement(s), +1 -1\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestReadBodyShowsLiteralCarriageReturn(t *testing.T) {
	data := ToolResultData{Content: "     2\tleft\rright\n", Width: 40}
	compact := xansi.Strip(renderNestedReadResultInline(data))
	data.Expanded = true
	expanded := xansi.Strip(renderNestedReadResultInline(data))
	if strings.Contains(compact, "left") || !strings.Contains(compact, "1 line") || !strings.HasSuffix(expanded, compact) || !strings.Contains(expanded, "  ┊      2    left␍right") {
		t.Fatalf("compact %q, expanded %q", compact, expanded)
	}
}

// A carriage return left in the line lands inside the "┊" gutter, and the
// terminal restarts the row at column 0. git rebase is where people see it.
func TestNestedToolBodyDropsOverwrittenProgress(t *testing.T) {
	body := renderNestedToolBody("Rebasing (1/1)\rAuto-merging f.txt\nCONFLICT (content): Merge conflict", 80)

	if strings.Contains(body, "\r") {
		t.Fatalf("a carriage return reached the frame: %q", body)
	}
	if strings.Contains(body, "Rebasing (1/1)") {
		t.Errorf("overwritten progress was rendered: %q", body)
	}
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if !strings.HasPrefix(xansi.Strip(line), "  ┊ ") && strings.TrimSpace(xansi.Strip(line)) != "" {
			t.Errorf("line escaped the gutter: %q", line)
		}
	}
	for _, want := range []string{"Auto-merging f.txt", "CONFLICT (content): Merge conflict"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lost %q: %q", want, body)
		}
	}
}

// CRLF is a line ending, not an overwrite: the text before it is what was shown.
func TestNestedToolBodyKeepsCRLFContent(t *testing.T) {
	body := renderNestedToolBody("first line\r\nsecond line\r", 80)
	for _, want := range []string{"first line", "second line"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lost %q: %q", want, body)
		}
	}
}

// A row wider than the terminal is wrapped here, gutter and all: left to the
// terminal, the continuation restarts at column 0 outside the "┊" connector.
func TestNestedToolBodyWrapsLongRowsInsideGutter(t *testing.T) {
	const width = 40
	long := "test\tpending\t0\thttps://github.com/genai-io/san/actions/runs/34793367902/job/103821690303"
	body := renderNestedToolBody(long, width)

	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("long row was not wrapped: %q", body)
	}
	var joined strings.Builder
	for _, line := range lines {
		plain := xansi.Strip(line)
		if !strings.HasPrefix(plain, "  ┊ ") {
			t.Errorf("continuation escaped the gutter: %q", plain)
		}
		if w := lipgloss.Width(plain); w > width {
			t.Errorf("row is %d cells wide, terminal is %d: %q", w, width, plain)
		}
		joined.WriteString(strings.TrimPrefix(plain, "  ┊ "))
	}
	// A break eats the whitespace it lands on, so compare everything else.
	if got, want := strings.Join(strings.Fields(joined.String()), ""), strings.Join(strings.Fields(long), ""); got != want {
		t.Errorf("wrapped rows lost content:\n got %q\nwant %q", got, want)
	}
}
