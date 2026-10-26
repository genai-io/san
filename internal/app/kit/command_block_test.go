package kit

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

func TestCommandBlockIsInsetRectangle(t *testing.T) {
	oldProfile, oldDark := lipgloss.Writer.Profile, darkModeVal
	lipgloss.Writer.Profile = colorprofile.TrueColor
	t.Cleanup(func() { lipgloss.Writer.Profile, darkModeVal = oldProfile, oldDark })
	for _, theme := range []string{"light", "dark"} {
		InitTheme(theme)
		for _, width := range []int{8, 10, 12, 32, 40, 100} {
			command := "echo first\n\n  echo second \\\n    " + strings.Repeat("中文", 30) + "\n"
			raw := RenderCommandBlock(command, width)
			rows := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
			if strings.Count(ansi.Strip(raw), "$ ") != 1 || strings.Contains(ansi.Strip(raw), "…") {
				t.Fatalf("%s width=%d lost the prompt or command: %q", theme, width, raw)
			}
			style := lipgloss.NewStyle().Foreground(CurrentTheme.Text).Background(CurrentTheme.CommandBg)
			for _, row := range rows {
				plain := ansi.Strip(row)
				if ansi.StringWidth(plain) != width-1 || !strings.HasPrefix(row, "  \x1b[") {
					t.Fatalf("%s width=%d has a ragged edge or colored outer margin: %q", theme, width, row)
				}
				if row != "  "+style.Render(plain[2:]) {
					t.Fatalf("background does not cover the whole inset row: %q", row)
				}
			}
			if !strings.HasPrefix(ansi.Strip(rows[0]), "  $ ") {
				t.Fatal("command prompt must start at the inset edge without extra padding")
			}
			if strings.TrimSpace(ansi.Strip(rows[len(rows)-1])) != "" {
				t.Fatal("explicit trailing blank command row was dropped")
			}
		}
	}
}

func TestCommandBlockAddsNoVerticalPadding(t *testing.T) {
	got := ansi.Strip(RenderCommandBlock("first\n\n  third", 40))
	rows := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(rows) != 3 || !strings.HasPrefix(rows[0], "  $ first") || strings.TrimSpace(rows[1]) != "" || !strings.HasPrefix(rows[2], "      third") {
		t.Fatalf("source rows or indentation changed: %q", rows)
	}
}

// A wrapped command keeps word boundaries where possible and every original
// character, including spaces that are meaningful inside quoted arguments.
func TestWrapCommandLinePreservesContents(t *testing.T) {
	for _, tc := range []struct {
		in    string
		width int
		want  string
	}{
		{"echo compile; sleep 0.2", 14, "echo compile; \nsleep 0.2"},
		{"printf 'A     B'", 12, "printf 'A   \n  B'"},
		{"abcdefghij", 4, "abcd\nefgh\nij"},
		{"中文没有空格", 4, "中文\n没有\n空格"},
		{"中文", 1, "中\n文"},
		{"echo 很长很长的一个词", 8, "echo 很\n长很长的\n一个词"},
		{"x = set(abcdefghijklm)", 16, "x = set(abcdefgh\nijklm)"},
	} {
		got := wrapCommandLine(tc.in, tc.width)
		if got != tc.want || strings.ReplaceAll(got, "\n", "") != tc.in {
			t.Errorf("wrapCommandLine(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
		}
	}
}

func TestWrapCommandLinePreservesLongCommandContents(t *testing.T) {
	for _, width := range []int{25, 33, 73, 93} {
		for _, command := range []string{
			"printf 'A     B' " + strings.Repeat("argument ", 24),
			"  " + strings.Repeat("中文", 60),
			"    " + strings.Repeat("next ", 30) + "\\",
			strings.Repeat(" ", 120),
		} {
			wrapped := wrapCommandLine(command, width)
			if strings.ReplaceAll(wrapped, "\n", "") != command {
				t.Fatalf("width %d changed command characters: %q", width, wrapped)
			}
			for row := range strings.SplitSeq(wrapped, "\n") {
				if ansi.StringWidth(row) > width {
					t.Fatalf("width %d overflowed: %q", width, row)
				}
			}
		}
	}
}
