// Markdown renderer using glamour for styled terminal output.
// Tables are rendered separately with lipgloss/table for full border control,
// since glamour hardcodes outer borders off (ansi/table.go setBorders).
package conv

import (
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/genai-io/san/internal/app/kit"
)

// trimStyledBlankLines drops leading and trailing lines that are blank once
// ANSI styling is removed. glamour pads a block with full-width margin lines
// of styled spaces — plain strings.TrimSpace can't see them because they start
// with a color escape — and committed block-by-block those margins would stack
// into extra vertical gaps between rendered blocks.
func trimStyledBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(xansi.Strip(lines[start])) == "" {
		start++
	}
	for end > start && strings.TrimSpace(xansi.Strip(lines[end-1])) == "" {
		end--
	}
	if start == 0 && end == len(lines) {
		return s
	}
	return strings.Join(lines[start:end], "\n")
}

// MDRenderer renders markdown content to styled terminal output using
// glamour. Safe for use from a single goroutine only — the TUI's
// rendering path is single-threaded (bubbletea Update/View). The mutex
// only protects the dark/light rebuild path in case a future caller
// reuses the renderer from a background goroutine.
type MDRenderer struct {
	mu       sync.Mutex
	renderer *glamour.TermRenderer
	width    int
	darkBg   bool // tracks last known terminal background to detect theme changes
}

// NewMDRenderer creates a new markdown renderer with the given terminal width.
// The width passed should be the raw terminal column count; the renderer
// subtracts aiIndentWidth internally so glamour wraps exactly at the
// visible boundary after the "● " prompt icon + indent are applied.
func NewMDRenderer(width int) *MDRenderer {
	w := max(width-4, minWrapWidth)
	dark := kit.IsDarkBackground()
	r := buildGlamourRenderer(w, dark)
	return &MDRenderer{renderer: r, width: w, darkBg: dark}
}

// buildGlamourRenderer constructs a glamour TermRenderer for the given width and background.
func buildGlamourRenderer(width int, dark bool) *glamour.TermRenderer {
	var style ansi.StyleConfig
	if dark {
		style = styles.DarkStyleConfig
	} else {
		style = styles.LightStyleConfig
	}
	customizeStyle(&style, width)

	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
		glamour.WithChromaFormatter("terminal256"),
	)
	if err != nil {
		fallback := styles.DarkStyle
		if !dark {
			fallback = styles.LightStyle
		}
		r, _ = glamour.NewTermRenderer(glamour.WithStandardStyle(fallback))
	}
	return r
}

// rebuildIfNeeded recreates the glamour renderer when the terminal background changes.
func (r *MDRenderer) rebuildIfNeeded() {
	dark := kit.IsDarkBackground()
	if dark != r.darkBg {
		r.renderer = buildGlamourRenderer(r.width, dark)
		r.darkBg = dark
	}
}

// Render parses markdown source and returns styled terminal output.
// Tables are extracted and rendered with lipgloss/table for full border control,
// everything else (including code blocks) goes through glamour natively.
func (r *MDRenderer) Render(content string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rebuildIfNeeded()
	// Normalize paragraph line breaks: LLMs often hard-wrap at ~80 columns,
	// producing softbreaks that glamour preserves as newlines. Joining them
	// lets glamour re-wrap at the actual terminal width.
	content = normalizeLineBreaks(content)
	segments := splitTables(content)

	var parts []string
	for _, seg := range segments {
		switch seg.kind {
		case segTable:
			parts = append(parts, r.renderTable(seg.content))
		default:
			rendered, err := r.renderer.Render(seg.content)
			if err != nil {
				parts = append(parts, seg.content)
			} else {
				rendered = collapseBlankLines(rendered)
				parts = append(parts, strings.TrimRight(rendered, "\n"))
			}
		}
	}

	result := strings.TrimRight(strings.Join(parts, ""), "\n")
	result = strings.TrimLeft(result, "\n")
	return result, nil
}

// segmentKind identifies what type of markdown block a segment contains.
type segmentKind int

const (
	segPlain segmentKind = iota
	segTable
)

// segment represents a piece of markdown content.
type segment struct {
	content string
	kind    segmentKind
}

// splitTables splits markdown content into table and non-table segments.
// Tables are rendered separately with lipgloss/table for full border control.
func splitTables(content string) []segment {
	lines := strings.Split(content, "\n")
	var segments []segment
	var plain []string

	i := 0
	for i < len(lines) {
		if isTableLine(lines[i]) {
			tableEnd := findTableEnd(lines, i)
			if tableEnd > i+1 && hasTableSeparator(lines, i, tableEnd) {
				if len(plain) > 0 {
					segments = append(segments, segment{content: strings.Join(plain, "\n"), kind: segPlain})
					plain = nil
				}
				tableLines := strings.Join(lines[i:tableEnd], "\n")
				segments = append(segments, segment{content: tableLines, kind: segTable})
				i = tableEnd
				continue
			}
		}
		plain = append(plain, lines[i])
		i++
	}

	if len(plain) > 0 {
		segments = append(segments, segment{content: strings.Join(plain, "\n"), kind: segPlain})
	}
	return segments
}

// isTableLine checks if a line looks like a markdown table line (starts with |).
func isTableLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "|") && strings.Contains(trimmed[1:], "|")
}

// findTableEnd finds the end index (exclusive) of consecutive table lines.
func findTableEnd(lines []string, start int) int {
	i := start
	for i < len(lines) && isTableLine(lines[i]) {
		i++
	}
	return i
}

// hasTableSeparator checks if there's a separator line (|---|) in the range.
func hasTableSeparator(lines []string, start, end int) bool {
	for i := start; i < end; i++ {
		trimmed := strings.TrimSpace(lines[i])
		// A separator line contains |, -, and optionally : for alignment
		cleaned := strings.NewReplacer("|", "", "-", "", ":", "", " ", "").Replace(trimmed)
		if cleaned == "" && strings.Contains(trimmed, "-") {
			return true
		}
	}
	return false
}

// renderTable renders a markdown table using lipgloss/table with full borders.
func (r *MDRenderer) renderTable(content string) string {
	headers, rows := parseMarkdownTable(content)
	if len(headers) == 0 {
		return content
	}

	borderColor := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Separator)
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(kit.CurrentTheme.TextBright)

	t := table.New().
		Headers(headers...).
		Rows(rows...).
		Border(lipgloss.RoundedBorder()).
		BorderStyle(borderColor).
		BorderTop(true).
		BorderBottom(true).
		BorderLeft(true).
		BorderRight(true).
		BorderHeader(true).
		BorderColumn(true).
		BorderRow(true).
		Width(r.width).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return headerStyle
			}
			return lipgloss.NewStyle().Foreground(kit.CurrentTheme.TextBright)
		})

	return "\n" + t.String() + "\n"
}

// parseMarkdownTable extracts headers and rows from a markdown table string.
func parseMarkdownTable(content string) ([]string, [][]string) {
	lines := strings.Split(content, "\n")
	if len(lines) < 2 {
		return nil, nil
	}

	var headers []string
	var rows [][]string
	headerParsed := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Check if this is a separator line (|---|---|)
		cleaned := strings.NewReplacer("|", "", "-", "", ":", "", " ", "").Replace(trimmed)
		if cleaned == "" && strings.Contains(trimmed, "-") {
			headerParsed = true
			continue
		}

		cells := parseTableRow(trimmed)
		if !headerParsed {
			headers = cells
		} else {
			rows = append(rows, cells)
		}
	}

	return headers, rows
}

// parseTableRow splits a markdown table row into cells and renders inline markdown.
func parseTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")

	parts := strings.Split(trimmed, "|")
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = renderInlineMarkdown(strings.TrimSpace(p))
	}
	return cells
}

// renderInlineMarkdown renders inline markdown elements: `code`, **bold**, *italic*, [text](url).
func renderInlineMarkdown(text string) string {
	var result strings.Builder
	i := 0
	for i < len(text) {
		// Inline code: `...`
		if text[i] == '`' {
			end := strings.Index(text[i+1:], "`")
			if end != -1 {
				code := text[i+1 : i+1+end]
				codeStyle := lipgloss.NewStyle().Foreground(kit.CurrentTheme.Accent)
				result.WriteString(codeStyle.Render(code))
				i += end + 2
				continue
			}
		}
		// Link: [text](url)
		if text[i] == '[' {
			closeBracket := strings.Index(text[i+1:], "](")
			if closeBracket != -1 {
				linkText := text[i+1 : i+1+closeBracket]
				urlStart := i + 1 + closeBracket + 2
				closeParen := strings.Index(text[urlStart:], ")")
				if closeParen != -1 {
					url := text[urlStart : urlStart+closeParen]
					linkStyle := lipgloss.NewStyle().
						Foreground(kit.CurrentTheme.Primary).
						Underline(true)
					styled := linkStyle.Render(linkText)
					result.WriteString("\x1b]8;;" + url + "\x1b\\" + styled + "\x1b]8;;\x1b\\")
					i = urlStart + closeParen + 1
					continue
				}
			}
		}
		// Bold: **...**
		if i+1 < len(text) && text[i] == '*' && text[i+1] == '*' {
			end := strings.Index(text[i+2:], "**")
			if end != -1 {
				bold := text[i+2 : i+2+end]
				boldStyle := lipgloss.NewStyle().Bold(true)
				result.WriteString(boldStyle.Render(bold))
				i += end + 4
				continue
			}
		}
		// Italic: *...*
		if text[i] == '*' {
			end := strings.Index(text[i+1:], "*")
			if end != -1 {
				italic := text[i+1 : i+1+end]
				italicStyle := lipgloss.NewStyle().Italic(true)
				result.WriteString(italicStyle.Render(italic))
				i += end + 2
				continue
			}
		}
		result.WriteByte(text[i])
		i++
	}
	return result.String()
}

// adaptiveColorHex resolves an AdaptiveColor to its hex string based on the
// current terminal background. Used for glamour StyleConfig which requires *string.
func adaptiveColorHex(c kit.AdaptiveColor) string {
	if kit.IsDarkBackground() {
		return c.Dark
	}
	return c.Light
}

// customizeStyle adjusts glamour's default style for a clean, unified look.
func customizeStyle(s *ansi.StyleConfig, width int) {
	blue := adaptiveColorHex(kit.CurrentTheme.Primary)
	text := adaptiveColorHex(kit.CurrentTheme.Text)
	textDim := adaptiveColorHex(kit.CurrentTheme.TextDim)

	// Document: set foreground color, no margin (paragraph spacing handled by glamour block prefix/suffix)
	margin := uint(0)
	s.Document.Margin = &margin
	s.Document.Color = &text
	s.Document.BlockPrefix = ""
	s.Document.BlockSuffix = ""

	// Headings: themed blue, bold, no extra prefix/suffix markers
	s.H1.Prefix = ""
	s.H1.Suffix = ""
	s.H1.Color = &blue
	s.H1.BackgroundColor = nil
	s.H1.Bold = boolPtr(true)
	s.H2.Prefix = ""
	s.H2.Color = &blue
	s.H2.Bold = boolPtr(true)
	s.H3.Prefix = ""
	s.H3.Color = &blue
	s.H3.Bold = boolPtr(true)
	s.Heading.BlockSuffix = "\n"
	s.H4.Prefix = ""
	s.H5.Prefix = ""
	s.H6.Prefix = ""

	// BlockQuote: muted color with standard │ indent token
	s.BlockQuote.Color = &textDim
	s.BlockQuote.Indent = uintPtr(1)
	s.BlockQuote.IndentToken = stringPtr("│ ")

	// Horizontal rule: full-width thin line. Kept on the faint Border tone (not
	// muted) so a section divider recedes instead of competing with the prose —
	// lighter on the light theme, subtler on the dark one.
	border := adaptiveColorHex(kit.CurrentTheme.Border)
	hr := strings.Repeat("─", width)
	s.HorizontalRule.Format = "\n" + hr + "\n"
	s.HorizontalRule.Color = &border

	// Inline code: no background, accent color
	accent := adaptiveColorHex(kit.CurrentTheme.Accent)
	s.Code.BackgroundColor = nil
	s.Code.Prefix = ""
	s.Code.Suffix = ""
	s.Code.Color = &accent

	// Code blocks: remove Chroma background color for cleaner look
	if s.CodeBlock.Chroma != nil {
		s.CodeBlock.Chroma.Background = ansi.StylePrimitive{}
		s.CodeBlock.Chroma.Error = ansi.StylePrimitive{}
	}
}

func boolPtr(b bool) *bool { return &b }

var reTripleNewlines = regexp.MustCompile(`\n{3,}`)

func collapseBlankLines(s string) string {
	return reTripleNewlines.ReplaceAllString(s, "\n\n")
}
func uintPtr(u uint) *uint       { return &u }
func stringPtr(s string) *string { return &s }

// CompletedBlockBoundary returns the byte offset in content up to which the
// text forms complete markdown blocks that are safe to render and commit to
// scrollback. Content after the boundary is the still-streaming block and must
// stay in the live view until more arrives. Two things close a block: a blank
// line outside a fenced code block (it terminates the preceding blocks), and a
// closing code fence (the block is complete the moment it closes). The trailing
// block is never included — the turn-end commit flushes whatever remains.
func CompletedBlockBoundary(content string) int {
	boundary := 0
	offset := 0
	inFence := false
	for _, line := range strings.SplitAfter(content, "\n") {
		offset += len(line)
		// A line without a trailing newline is the last, still-streaming line:
		// never a boundary, whatever it contains.
		if !strings.HasSuffix(line, "\n") {
			continue
		}
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
			inFence = !inFence
			if !inFence {
				boundary = offset // a code block is complete once its fence closes
			}
		case !inFence && trimmed == "":
			boundary = offset // a blank line terminates the preceding blocks
		}
	}
	return boundary
}

// normalizeLineBreaks joins single-newline breaks within plain paragraphs so
// that glamour's word-wrap can reflow text to the terminal width. Structural
// markdown lines (headers, lists, blockquotes, code blocks, tables) and blank
// lines (paragraph separators) are preserved as-is.
func normalizeLineBreaks(content string) string {
	lines := strings.Split(content, "\n")
	var result []string
	inCodeBlock := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track fenced code blocks
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inCodeBlock = !inCodeBlock
			result = append(result, line)
			continue
		}
		if inCodeBlock {
			result = append(result, line)
			continue
		}

		// Blank line = paragraph separator
		if trimmed == "" {
			result = append(result, line)
			continue
		}

		// Indented code block (4+ spaces or tab)
		if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			result = append(result, line)
			continue
		}

		// Structural markdown lines: preserve as-is
		if isMarkdownStructural(trimmed) {
			result = append(result, line)
			continue
		}

		// Try to join with the previous line if it was a plain paragraph line
		if i > 0 && len(result) > 0 {
			prev := result[len(result)-1]
			prevTrimmed := strings.TrimSpace(prev)
			if prevTrimmed != "" &&
				!strings.HasPrefix(prevTrimmed, "```") && !strings.HasPrefix(prevTrimmed, "~~~") &&
				!strings.HasPrefix(prev, "    ") && !strings.HasPrefix(prev, "\t") &&
				!isMarkdownStructural(prevTrimmed) &&
				!strings.HasSuffix(prev, "  ") {
				// Don't insert a space between CJK lines — Chinese/Japanese/Korean
				// text doesn't use spaces between words.
				sep := " "
				if endsWithCJK(prevTrimmed) || startsWithCJK(trimmed) {
					sep = ""
				}
				result[len(result)-1] = prev + sep + trimmed
				continue
			}
		}

		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// isMarkdownStructural returns true for lines that start markdown block structures
// (headers, list items, blockquotes, table rows, thematic breaks).
func isMarkdownStructural(line string) bool {
	// Headers
	if strings.HasPrefix(line, "#") {
		return true
	}
	// Unordered lists
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "+ ") {
		return true
	}
	// Blockquotes
	if strings.HasPrefix(line, "> ") || line == ">" {
		return true
	}
	// Table rows
	if strings.HasPrefix(line, "|") {
		return true
	}
	// Thematic breaks (---, ***, ___)
	if isThematicBreak(line) {
		return true
	}
	// Ordered lists (digit(s) + . + space)
	return isOrderedListItem(line)
}

// isThematicBreak checks if line is a markdown thematic break (---, ***, ___).
func isThematicBreak(line string) bool {
	cleaned := strings.ReplaceAll(line, " ", "")
	if len(cleaned) < 3 {
		return false
	}
	return strings.Count(cleaned, "-") == len(cleaned) ||
		strings.Count(cleaned, "*") == len(cleaned) ||
		strings.Count(cleaned, "_") == len(cleaned)
}

// isOrderedListItem checks if line starts with a numbered list marker (e.g., "1. ").
func isOrderedListItem(line string) bool {
	for i, c := range line {
		if c >= '0' && c <= '9' {
			continue
		}
		return c == '.' && i > 0 && i+1 < len(line) && line[i+1] == ' '
	}
	return false
}

// isCJK reports whether r is a CJK (Chinese/Japanese/Korean) character.
func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0x3400 && r <= 0x4DBF) || // CJK Extension A
		(r >= 0x20000 && r <= 0x2A6DF) || // CJK Extension B
		(r >= 0x3000 && r <= 0x303F) || // CJK Symbols and Punctuation
		(r >= 0xFF00 && r <= 0xFFEF) // Halfwidth/Fullwidth Forms
}

// endsWithCJK reports whether s ends with a CJK character.
func endsWithCJK(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	return r != utf8.RuneError && isCJK(r)
}

// startsWithCJK reports whether s starts with a CJK character.
func startsWithCJK(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return r != utf8.RuneError && isCJK(r)
}
