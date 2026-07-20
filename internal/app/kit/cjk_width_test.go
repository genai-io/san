package kit

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// A CJK rune occupies two terminal columns but is one rune and three bytes.
// Panels that measure a column budget with len() or utf8.RuneCountInString
// misalign their columns and push box borders past the corner, so these pin
// that the shared helpers answer in columns.

func TestTruncateTextBudgetsDisplayColumns(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		max  int
	}{
		{"ascii", "git-commit-helper", 10},
		{"cjk", "代码审查助手", 10},
		{"mixed", "review-代码审查", 10},
	} {
		got := TruncateText(tc.text, tc.max)
		if w := lipgloss.Width(got); w > tc.max {
			t.Errorf("%s: TruncateText(%q, %d) renders %d columns, over budget: %q",
				tc.name, tc.text, tc.max, w, got)
		}
	}
}

// TruncateKeepEnd spends the budget on the tail — a path's filename identifies
// it, the leading directories do not.
func TestTruncateKeepEndBudgetsDisplayColumnsAndKeepsTheTail(t *testing.T) {
	got := TruncateKeepEnd("项目/笔记/说明文档.md", 12)
	if w := lipgloss.Width(got); w > 12 {
		t.Errorf("renders %d columns, over budget: %q", w, got)
	}
	if !strings.HasSuffix(got, ".md") {
		t.Errorf("lost the tail that identifies the path: %q", got)
	}
	if !strings.HasPrefix(got, "…") {
		t.Errorf("want a leading ellipsis marking the elision: %q", got)
	}
}

// Padding a name to a column width has to measure the name in columns too, or
// a CJK row's following columns start further left than every ASCII row's.
// This is the shape the skill and agent panels use.
func TestColumnPaddingAlignsCJKWithASCII(t *testing.T) {
	const col = 16
	for _, name := range []string{"git-commit", "代码审查", "review-审查"} {
		truncated := TruncateText(name, col)
		padded := truncated + strings.Repeat(" ", max(0, col-lipgloss.Width(truncated)))
		if w := lipgloss.Width(padded); w != col {
			t.Errorf("%q padded to %d columns, want exactly %d", name, w, col)
		}
	}
}

// FormatAlignedRow is the canonical helper; it already measures in columns.
func TestFormatAlignedRowKeepsTheInfoColumnStable(t *testing.T) {
	ascii := FormatAlignedRow("●", "git-commit", 16, "info")
	cjk := FormatAlignedRow("●", "代码审查", 16, "info")
	if strings.Index(ascii, "info") == 0 || strings.Index(cjk, "info") == 0 {
		t.Fatal("test setup: info marker not found")
	}
	aw := lipgloss.Width(ascii[:strings.Index(ascii, "info")])
	cw := lipgloss.Width(cjk[:strings.Index(cjk, "info")])
	if aw != cw {
		t.Errorf("info column starts at %d for ASCII but %d for CJK", aw, cw)
	}
}
