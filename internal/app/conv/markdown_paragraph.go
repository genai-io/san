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

// wrapParagraph lays out plain-text byte ranges, then copies the styled input
// once. Display-column slicing is both quadratic and unsafe when the slicing
// and measuring APIs disagree about an ASCII-start grapheme (such as a keycap).
func wrapParagraph(styled string, width int) string {
	if width <= 0 {
		return styled
	}
	type row struct{ start, end int }
	var rows []row
	plain := xansi.Strip(styled)
	offset := 0
	for _, line := range strings.Split(plain, "\n") {
		if strings.TrimSpace(line) == "" {
			rows = append(rows, row{offset, offset + len(line)})
			offset += len(line) + 1
			continue
		}
		start, end, columns := 0, 0, 0
		flush := func() {
			rows = append(rows, row{offset + start, offset + end})
		}
		rest, state, pos := line, -1, 0
		for len(rest) > 0 {
			segment := rest
			length, boundary := 0, uniseg.LineDontBreak
			for len(rest) > 0 && boundary == uniseg.LineDontBreak {
				var cluster string
				var boundaries int
				cluster, rest, boundaries, state = uniseg.StepString(rest, state)
				length += len(cluster)
				boundary = boundaries & uniseg.MaskLine
			}
			segment = segment[:length]
			visible := strings.TrimRight(segment, " \t")
			segmentWidth := xansi.StringWidth(visible)
			if columns+segmentWidth > width {
				if end > start {
					flush()
				}
				// Discard leading wrap whitespace rather than emit an empty
				// row when indentation alone makes the next word overflow.
				start, columns = pos, 0
			}
			// Only an oversized word needs emergency grapheme breaks. Use the
			// same grapheme width API as StringWidth, including ASCII starters.
			if segmentWidth > width {
				for text, at := visible, pos; len(text) > 0; {
					cluster, cells := xansi.FirstGraphemeCluster(text, xansi.GraphemeWidth)
					if at > start && columns+cells > width {
						end = at
						flush()
						start, columns = at, 0
					}
					columns += cells
					at += len(cluster)
					text = text[len(cluster):]
				}
			} else {
				columns += segmentWidth
			}
			end = pos + len(visible)
			columns += xansi.StringWidth(segment[len(visible):])
			pos += len(segment)
			if boundary == uniseg.LineMustBreak && len(rest) > 0 {
				flush()
				start, end, columns = pos, pos, 0
			}
		}
		if end > start {
			flush()
		}
		offset += len(line) + 1
	}

	var out strings.Builder
	out.Grow(len(styled))
	var style paragraphStyle
	position, current := 0, 0
	for len(styled) > 0 {
		// Decode only escape sequences, never text: DecodeSequence treats
		// printable ASCII as individual bytes, not complete graphemes.
		if styled[0] == '\x1b' {
			sequence, _, n, _ := xansi.DecodeSequence(styled, xansi.NormalState, nil)
			out.WriteString(sequence)
			style.consume(sequence)
			styled = styled[n:]
			continue
		}
		for current+1 < len(rows) && position >= rows[current+1].start {
			style.newline(&out)
			current++
		}
		if position >= rows[current].start && position < rows[current].end {
			out.WriteByte(styled[0])
		}
		position++
		styled = styled[1:]
	}
	// A final empty source line has no text byte to trigger its boundary.
	for current+1 < len(rows) {
		style.newline(&out)
		current++
	}
	return out.String()
}

// Glamour resets SGR between styled spans. Retain the intervening sequences so
// partial resets and extended colors replay exactly, and isolate each row from
// gutters/padding (and from viewport clipping of preceding rows).
type paragraphStyle struct {
	sgr  string
	link string
}

func (s *paragraphStyle) consume(sequence string) {
	if strings.HasPrefix(sequence, "\x1b[") && strings.HasSuffix(sequence, "m") {
		if sequence == "\x1b[m" || sequence == "\x1b[0m" {
			s.sgr = ""
		} else {
			s.sgr += sequence
		}
	}
	if strings.HasPrefix(sequence, "\x1b]8;") {
		payload := strings.TrimSuffix(strings.TrimSuffix(sequence[4:], "\x1b\\"), "\a")
		_, url, ok := strings.Cut(payload, ";")
		if ok && url != "" {
			s.link = sequence
		} else {
			s.link = ""
		}
	}
}

func (s *paragraphStyle) newline(out *strings.Builder) {
	if s.link != "" {
		out.WriteString("\x1b]8;;\x1b\\")
	}
	if s.sgr != "" {
		out.WriteString("\x1b[0m")
	}
	out.WriteByte('\n')
	out.WriteString(s.sgr)
	out.WriteString(s.link)
}
