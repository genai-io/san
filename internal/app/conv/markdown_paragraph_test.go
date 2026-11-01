package conv

import (
	"fmt"
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
)

const screenshotParagraph = "梳理时发现几条提交路径的 Hook 行为不同，不能直接把 Hook 塞进所有消息入口，否则会改变 TUI 行为。另外，临时工作区被外部清理了，目前没有产生代码修改。我会换用新的隔离目录继续，保留现有路径差异，并用测试固定这些边界。\n"

func paragraphRows(s string) []string {
	var rows []string
	for _, row := range strings.Split(xansi.Strip(s), "\n") {
		if row = strings.TrimRight(row, " "); row != "" {
			rows = append(rows, row)
		}
	}
	return rows
}

func TestMDRendererCJKParagraphFillsWidth(t *testing.T) {
	for _, width := range []int{40, 80, 140, 160, 180} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			r := NewMDRenderer(width)
			out, err := r.Render(screenshotParagraph)
			if err != nil {
				t.Fatal(err)
			}
			rows := paragraphRows(out)
			if len(rows) < 2 {
				t.Fatalf("expected wrapped paragraph: %q", out)
			}
			if got := xansi.StringWidth(rows[0]); got < r.width-4 {
				t.Errorf("first line width %d, available %d: %q", got, r.width, rows[0])
			}
			for _, row := range rows {
				if xansi.StringWidth(row) > r.width {
					t.Errorf("overflow: %q", row)
				}
			}
			compact := func(s string) string { return strings.Join(strings.Fields(s), "") }
			if compact(strings.Join(rows, "")) != compact(screenshotParagraph) {
				t.Fatalf("paragraph text changed: %q", rows)
			}
		})
	}
}

func TestMDRendererCJKParagraphInlineStyles(t *testing.T) {
	r := NewMDRenderer(40)
	src := "中文开头 **English words** 和 *强调文字*，使用 `Hook` 与 [链接](https://example.com/a)，继续" + strings.Repeat("中文内容", 12)
	out, err := r.Render(src)
	if err != nil {
		t.Fatal(err)
	}
	wide, err := r.paragraphRenderer.Render(src)
	if err != nil {
		t.Fatal(err)
	}
	compact := func(s string) string { return strings.Join(strings.Fields(xansi.Strip(s)), "") }
	if compact(out) != compact(wide) {
		t.Fatalf("inline content changed:\n%s\n%s", out, wide)
	}
	if !strings.Contains(out, "\x1b[") {
		t.Fatal("lost ANSI styling")
	}
	for _, word := range []string{"English", "words", "Hook"} {
		if !strings.Contains(xansi.Strip(out), word) {
			t.Errorf("split English word %q", word)
		}
	}
	for _, row := range paragraphRows(out) {
		if xansi.StringWidth(row) > r.width {
			t.Errorf("overflow: %q", row)
		}
	}
}

func TestWrapParagraphPreservesHyperlinksAndGraphemes(t *testing.T) {
	const open = "\x1b]8;;https://example.com\x1b\\"
	const close = "\x1b]8;;\x1b\\"
	const cluster = "👩🏽‍💻"
	text := strings.Repeat("中文", 5) + "e\u0301" + cluster + strings.Repeat("文字", 8)
	styled := "\x1b[1m" + open + text + close + "\x1b[0m"
	out := wrapParagraph(styled, 13)
	if strings.ReplaceAll(xansi.Strip(out), "\n", "") != text {
		t.Fatalf("lost text: %q", out)
	}
	if !strings.Contains(out, open) || !strings.Contains(out, close) {
		t.Fatal("lost OSC hyperlink")
	}
	if !strings.Contains(xansi.Strip(out), cluster) || !strings.Contains(xansi.Strip(out), "e\u0301") {
		t.Fatalf("split grapheme: %q", out)
	}
	for _, row := range strings.Split(out, "\n") {
		if xansi.StringWidth(row) > 13 {
			t.Errorf("overflow: %q", row)
		}
	}
}

func TestMDRendererCJKParagraphHardBreaksAndBlankParagraphs(t *testing.T) {
	for _, hardBreak := range []string{"  \n", "\\\n"} {
		r := NewMDRenderer(80)
		src := "第一行" + hardBreak + "第二行\n\n第三段"
		out, err := r.Render(src)
		if err != nil {
			t.Fatal(err)
		}
		rows := strings.Split(strings.TrimSpace(xansi.Strip(out)), "\n")
		if len(rows) != 4 || strings.TrimSpace(rows[0]) != "第一行" || strings.TrimSpace(rows[1]) != "第二行" || strings.TrimSpace(rows[2]) != "" || strings.TrimSpace(rows[3]) != "第三段" {
			t.Errorf("lost breaks for %q: %q", hardBreak, rows)
		}
	}
}

func TestMDRendererCJKParagraphLeavesOtherBlocksNative(t *testing.T) {
	blocks := []string{
		"# 中文标题",
		"中文标题\n---",
		"```text\n" + strings.Repeat("中文代码", 20) + "\n```",
		"~~~\n中文代码\n~~~",
		"    中文缩进代码",
		"- English list\n- second item",
		"> English quote",
	}
	for _, block := range blocks {
		t.Run(block[:min(len(block), 15)], func(t *testing.T) {
			r := NewMDRenderer(40)
			// Compare the native block in a mixed document with its standalone render.
			native, err := r.renderer.Render(block)
			if err != nil {
				t.Fatal(err)
			}
			out, err := r.Render(screenshotParagraph + "\n" + block + "\n\n" + screenshotParagraph)
			if err != nil {
				t.Fatal(err)
			}
			want := trimStyledBlankLines(native)
			if !strings.Contains(out, want) {
				t.Fatalf("block changed:\nwant %q\ngot %q", want, out)
			}
		})
	}
}

func TestMDRendererCJKKeycapPreservesFinalCharacter(t *testing.T) {
	text := strings.Repeat("中1️⃣文 ", 13)
	out, err := NewMDRenderer(80).Render(text)
	if err != nil {
		t.Fatal(err)
	}
	compact := func(s string) string { return strings.Join(strings.Fields(xansi.Strip(s)), "") }
	if compact(out) != compact(text) {
		t.Fatalf("keycap paragraph lost text: %q", out)
	}
}

func TestWrapParagraphASCIIStartGraphemes(t *testing.T) {
	for _, cluster := range []string{"1️⃣", "#️⃣", "e\u0301"} {
		for _, width := range []int{3, 13, 78} {
			t.Run(fmt.Sprintf("%s/%d", cluster, width), func(t *testing.T) {
				text := strings.Repeat("中"+cluster+"文 ", 13)
				out := wrapParagraph(text, width)
				compact := func(s string) string { return strings.Join(strings.Fields(s), "") }
				if compact(out) != compact(text) {
					t.Fatalf("text changed: %q", out)
				}
				if strings.Count(out, cluster) != 13 {
					t.Fatalf("split grapheme: %q", out)
				}
				for _, row := range strings.Split(out, "\n") {
					if xansi.StringWidth(row) > width {
						t.Errorf("overflow at width %d: %q", width, row)
					}
				}
			})
		}
	}
	// Also exercise emergency breaks inside a single oversized English word.
	text := strings.Repeat("a1️⃣", 20)
	out := wrapParagraph(text, 5)
	if strings.ReplaceAll(out, "\n", "") != text || strings.Count(out, "1️⃣") != 20 {
		t.Fatalf("split oversized word grapheme: %q", out)
	}
}

func TestWrapParagraphLongStyledText(t *testing.T) {
	const open = "\x1b]8;id=test;https://example.com\x1b\\"
	const close = "\x1b]8;;\x1b\\"
	text := strings.Repeat("中文", 30000)
	out := wrapParagraph("\x1b[1m\x1b[38;2;12;34;56m"+open+text+close+"\x1b[0m", 80)
	if strings.ReplaceAll(xansi.Strip(out), "\n", "") != text {
		t.Fatal("long paragraph lost text")
	}
	rows := strings.Split(out, "\n")
	if len(rows) != 1500 {
		t.Fatalf("got %d rows, want 1500", len(rows))
	}
	for _, row := range rows {
		if !strings.HasPrefix(row, "\x1b[1m\x1b[38;2;12;34;56m"+open) || !strings.HasSuffix(row, close+"\x1b[0m") {
			t.Fatalf("row is not independently styled: %q", row)
		}
	}
}

func TestWrapParagraphLeadingWhitespace(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"  ab", "  ab"}, // Keep indentation when it fits.
		{" abcd", "abcd"},
		{"  abcde", "abcd\ne"},
		{"     abcd中文", "abcd\n中文"},
	} {
		if got := wrapParagraph(tc.input, 4); got != tc.want {
			t.Errorf("wrapParagraph(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}

	r := NewMDRenderer(40)
	word := strings.Repeat("a", 40)
	out, err := r.Render("`  " + word + "  `中文")
	if err != nil {
		t.Fatal(err)
	}
	rows := paragraphRows(out)
	if len(rows) != 2 || strings.TrimSpace(rows[0]) != word || strings.TrimSpace(rows[1]) != "中文" {
		t.Fatalf("leading code whitespace changed content or added a row: %q", out)
	}
	for _, row := range rows {
		if xansi.StringWidth(row) > r.width {
			t.Errorf("leading whitespace overflow: %q", row)
		}
	}
}

func TestWrapParagraphEmptyAndHardBreaks(t *testing.T) {
	for _, text := range []string{"", "\x1b[1m\x1b[0m", "\n", "\n\n", "中文\n", "中文\n\n文字", " \t\n中文"} {
		if got := wrapParagraph(text, 4); got != text {
			t.Errorf("wrapParagraph(%q) = %q", text, got)
		}
	}
	const styled = "\x1b[31m中文\n文字\x1b[0m"
	const want = "\x1b[31m中文\x1b[0m\n\x1b[31m文字\x1b[0m"
	if got := wrapParagraph(styled, 4); got != want {
		t.Fatalf("hard break lost row isolation: %q", got)
	}
	const transition = "\x1b[31m中文\x1b[0m\x1b[32m文字\x1b[0m"
	rows := strings.Split(wrapParagraph(transition, 4), "\n")
	if len(rows) != 2 || !strings.HasPrefix(rows[1], "\x1b[32m") || strings.Contains(rows[1], "\x1b[31m") {
		t.Fatalf("reset leaked prior style: %q", rows)
	}
}

func TestWrapParagraphPartialStyleReset(t *testing.T) {
	out := wrapParagraph("\x1b[1;31m中文\x1b[22m文字中文\x1b[0m", 4)
	rows := strings.Split(out, "\n")
	if len(rows) != 3 {
		t.Fatalf("unexpected rows: %q", out)
	}
	for _, row := range rows[1:] {
		if !strings.HasPrefix(row, "\x1b[1;31m\x1b[22m") || !strings.HasSuffix(row, "\x1b[0m") {
			t.Fatalf("lost partial SGR reset: %q", row)
		}
	}
}

func BenchmarkWrapParagraph(b *testing.B) {
	for _, size := range []int{6000, 60000} {
		for _, styled := range []bool{false, true} {
			b.Run(fmt.Sprintf("CJK%d/styled=%t", size, styled), func(b *testing.B) {
				unit := "中文"
				if styled {
					unit = "\x1b[1m\x1b]8;;https://example.com\x1b\\中文\x1b]8;;\x1b\\\x1b[0m"
				}
				input := strings.Repeat(unit, size/2)
				b.ReportAllocs()
				b.SetBytes(int64(len(input)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					wrapParagraph(input, 80)
				}
			})
		}
	}
}

func TestMDRendererCJKReferenceParagraphStaysNative(t *testing.T) {
	r := NewMDRenderer(40)
	src := "中文 [引用][ref] " + strings.Repeat("内容", 30) + "\n\n[ref]: https://example.com"
	got, err := r.renderPlain(src)
	if err != nil {
		t.Fatal(err)
	}
	want, err := r.renderer.Render(src)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatal("reference definitions must remain with their paragraph")
	}
}
