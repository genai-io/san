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
