package conv

import (
	"strings"
	"unicode"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// renderPlain leaves block layout to glamour, except for top-level CJK
// paragraphs. Glamour 2.0.1's paragraph wrapper treats an unspaced CJK run as
// one word, moving it wholesale onto the next line. Render those paragraphs
// without wrapping or padding, then apply Unicode line breaking to styled text.
// Goldmark supplies the boundaries; this is not another Markdown block parser.
func (r *MDRenderer) renderPlain(content string) (string, error) {
	if !strings.ContainsFunc(content, paragraphCJK) {
		return r.renderer.Render(content)
	}
	source := []byte(content)
	md := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.DefinitionList))
	context := parser.NewContext()
	doc := md.Parser().Parse(text.NewReader(source), parser.WithContext(context))
	// Reference definitions can live outside a paragraph's source span. Keep the
	// complete document together rather than lose their resolution when slicing.
	if len(context.References()) != 0 {
		return r.renderer.Render(content)
	}
	var out strings.Builder
	offset := 0
	for node := doc.FirstChild(); node != nil; node = node.NextSibling() {
		if node.Kind() != ast.KindParagraph || node.Lines().Len() == 0 {
			continue
		}
		lines := node.Lines()
		start, end := lines.At(0).Start, lines.At(lines.Len()-1).Stop
		paragraph := content[start:end]
		if !strings.ContainsFunc(paragraph, paragraphCJK) {
			continue
		}
		if start > offset {
			rendered, err := r.renderer.Render(content[offset:start])
			if err != nil {
				return "", err
			}
			out.WriteString(rendered)
		}
		rendered, err := r.paragraphRenderer.Render(paragraph)
		if err != nil {
			return "", err
		}
		out.WriteString("\n\n")
		out.WriteString(wrapParagraph(rendered, r.width))
		out.WriteString("\n\n")
		offset = end
	}
	if offset == 0 {
		return r.renderer.Render(content)
	}
	if offset < len(content) {
		rendered, err := r.renderer.Render(content[offset:])
		if err != nil {
			return "", err
		}
		out.WriteString(rendered)
	}
	return out.String(), nil
}

func paragraphCJK(r rune) bool {
	return isCJK(r) || unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// wrapParagraph uses Unicode's legal line boundaries, not whitespace-only
// words. ANSI slicing keeps styles and OSC hyperlinks intact, and Hardwrap
// splits only grapheme boundaries when a single word exceeds the whole width.
func wrapParagraph(styled string, width int) string {
	var output []string
	for _, line := range strings.Split(styled, "\n") {
		plain := xansi.Strip(line)
		if strings.TrimSpace(plain) == "" {
			output = append(output, line)
			continue
		}
		start, end, visibleEnd := 0, 0, 0
		flush := func() {
			output = append(output, xansi.Hardwrap(xansi.Cut(line, start, visibleEnd), width, false))
			start = end
		}
		state := -1
		for len(plain) > 0 {
			var segment string
			segment, plain, _, state = uniseg.FirstLineSegmentInString(plain, state)
			segmentWidth := xansi.StringWidth(segment)
			trimmedWidth := xansi.StringWidth(strings.TrimRight(segment, " \t"))
			if end > start && end-start+trimmedWidth > width {
				flush()
			}
			visibleEnd = end + trimmedWidth
			end += segmentWidth
		}
		if end > start {
			flush()
		}
	}
	return strings.Join(output, "\n")
}
